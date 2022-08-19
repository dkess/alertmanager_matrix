// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dchest/uniuri"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	pconfig "github.com/prometheus/common/config"
	"github.com/prometheus/common/promlog"
	"github.com/prometheus/common/promlog/flag"
	"gopkg.in/alecthomas/kingpin.v2"
	"gopkg.in/yaml.v3"

	"github.com/dkess/alertmanager_matrix/config"
)

type Alerts struct {
	Alerts            []Alert           `json:"alerts"`
	CommonAnnotations map[string]string `json:"commonAnnotations"`
	CommonLabels      map[string]string `json:"commonLabels"`
	ExternalURL       string            `json:"externalURL"`
	GroupKey          string            `json:"groupKey"`
	GroupLabels       map[string]string `json:"groupLabels"`
	Receiver          string            `json:"receiver"`
	Status            string            `json:"status"`
	TruncatedAlerts   int               `json:"truncatedAlerts"`
	Version           string            `json:"version"`
}

type Alert struct {
	Annotations  map[string]string `json:"annotations"`
	EndsAt       time.Time         `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
	Labels       map[string]string `json:"labels"`
	StartsAt     time.Time         `json:"startsAt"`
	Status       string            `json:"status"`
}

var (
	Version string

	sc = &config.SafeConfig{
		C: &config.Config{},
	}

	configFile    = kingpin.Flag("config.file", "alertmanager_matrix configuration file.").Default("ammatrix.yml").String()
	listenAddress = kingpin.Flag("web.listen-address", "The address to listen on for HTTP requests.").Default(":9751").String()
	configCheck   = kingpin.Flag("config.check", "If true validate the config file and then exit.").Default().Bool()
)

func showErr(w http.ResponseWriter, logger log.Logger, msg string, err error) {
	level.Error(logger).Log("msg", msg, "err", err)
	http.Error(w, fmt.Sprintf("%s: %v", msg, err), 500)
}

func sendToMatrix(ctx context.Context, httpClientConfig pconfig.HTTPClientConfig, homeserver_url, roomID, message string) (*http.Response, error) {
	matrixData := struct {
		Body    string `json:"body"`
		MsgType string `json:"msgtype"`
	}{Body: message, MsgType: "m.text"}

	postData, err := json.Marshal(matrixData)
	if err != nil {
		return nil, fmt.Errorf("JSON marshal: %w", err)
	}

	rt, err := pconfig.NewRoundTripperFromConfig(httpClientConfig, "matrix_client", pconfig.WithHTTP2Disabled())
	if err != nil {
		return nil, fmt.Errorf("Error generating HTTP round tripper: %w", err)
	}

	matrixRequest, err := http.NewRequestWithContext(
		ctx,
		"PUT",
		fmt.Sprintf("%s/_matrix/client/r0/rooms/%s/send/m.room.message/%s", homeserver_url, roomID, uniuri.New()),
		bytes.NewBuffer(postData),
	)
	if err != nil {
		return nil, fmt.Errorf("HTTP request construction: %w", err)
	}

	matrixRequest.Header.Set("Content-Type", "application/json")

	// Mark the request as replayable. No idempotency header needed because we provide a Matrix transaction ID.
	// See "X-Idempotency-Key" in net/http docs.
	matrixRequest.Header["X-Idempotency-Key"] = nil

	return rt.RoundTrip(matrixRequest)
}

func init() {
	prometheus.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Namespace: "alertmanager_matrix",
			Name:      "build_info",
			Help:      "A metric with a constant '1' value labeled by version",
			ConstLabels: prometheus.Labels{
				"version": Version,
			},
		},
		func() float64 { return 1 },
	))
}

func main() {
	os.Exit(run())
}

func run() int {
	promlogConfig := &promlog.Config{}
	flag.AddFlags(kingpin.CommandLine, promlogConfig)
	kingpin.Version(Version)
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()
	logger := promlog.New(promlogConfig)

	level.Info(logger).Log("msg", "Starting alertmanager_matrix", "version", Version)

	if err := sc.ReloadConfig(*configFile); err != nil {
		level.Error(logger).Log("msg", "Error loading config", "err", err)
		return 1
	}

	if *configCheck {
		level.Info(logger).Log("msg", "Config file is ok exiting...")
		return 0
	}

	level.Info(logger).Log("msg", "Loaded config file")

	hup := make(chan os.Signal, 1)
	reloadCh := make(chan chan error)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-hup:
				if err := sc.ReloadConfig(*configFile); err != nil {
					level.Error(logger).Log("msg", "Error reloading config", "err", err)
					continue
				}
				level.Info(logger).Log("msg", "Reloaded config file")
			case rc := <-reloadCh:
				if err := sc.ReloadConfig(*configFile); err != nil {
					level.Error(logger).Log("msg", "Error reloading config", "err", err)
					rc <- err
				} else {
					level.Info(logger).Log("msg", "Reloaded config file")
					rc <- nil
				}
			}
		}
	}()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		sc.RLock()
		conf := sc.C
		sc.RUnlock()

		b, err := ioutil.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}
		defer r.Body.Close()

		roomID := r.URL.Query().Get("room_id")
		if roomID == "" {
			http.Error(w, "No room_id specified", 400)
			return
		}

		var as Alerts
		if err := json.Unmarshal(b, &as); err != nil {
			http.Error(w, fmt.Sprintf("Couldn't unmarshal JSON: %v", err), 400)
			return
		}

		var msgBody bytes.Buffer
		if err := conf.TextTemplate.Execute(&msgBody, as); err != nil {
			showErr(w, logger, "Template execution", err)
			return
		}

		matrixResponse, err := sendToMatrix(r.Context(), conf.MatrixHTTPConfig, conf.HomeserverURL.String(), roomID, msgBody.String())
		if err != nil {
			showErr(w, logger, "Request to matrix", err)
			return
		}

		matrixResponseBody, err := ioutil.ReadAll(matrixResponse.Body)
		if err != nil {
			showErr(w, logger, "Read response body from Matrix", err)
			return
		}

		w.WriteHeader(matrixResponse.StatusCode)

		if n, err := w.Write(matrixResponseBody); err != nil {
			level.Warn(logger).Log("msg", "Write error", "bytes", n, "err", err)
			return
		}
	})
	http.Handle("/metrics", promhttp.Handler())
	http.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		sc.RLock()
		c, err := yaml.Marshal(sc.C)
		sc.RUnlock()
		if err != nil {
			level.Warn(logger).Log("msg", "Error marshalling configuration", "err", err)
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write(c)
	})

	http.HandleFunc("/-/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			fmt.Fprintf(w, "This endpoint requires a POST request.\n")
			return
		}

		rc := make(chan error)
		reloadCh <- rc
		if err := <-rc; err != nil {
			http.Error(w, fmt.Sprintf("failed to reload config: %s", err), http.StatusInternalServerError)
		}
	})
	http.HandleFunc("/-/healthy", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})
	http.HandleFunc("/-/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "OK")
	})

	srv := http.Server{Addr: *listenAddress}
	srvc := make(chan struct{})
	term := make(chan os.Signal, 1)
	signal.Notify(term, os.Interrupt, syscall.SIGTERM)

	go func() {
		level.Info(logger).Log("msg", "Listening on address", "address", *listenAddress)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			level.Error(logger).Log("msg", "Error starting HTTP server", "err", err)
			close(srvc)
		}
	}()

	for {
		select {
		case <-term:
			level.Info(logger).Log("msg", "Received SIGTERM, exiting gracefully...")
			return 0
		case <-srvc:
			return 1
		}
	}
}
