# Alertmanager Matrix

Alertmanager Matrix provides a webhook endpoint that takes alerts and sends them to a Matrix room.

## Running this software

    ./alertmanager_matrix <flags>

## Building the software

    make

## Packages

This software is available in the AUR: [`alertmanager-matrix`](https://aur.archlinux.org/packages/alertmanager-matrix/)

If you're interested in a package for your favorite distro, please let me know!

## Configuration

Alertmanager Matrix is configured via a configuration file and command-line flags (such as what configuration file to load, what port to listen on, and the logging format and level).

The configuration file is written in [YAML format](http://en.wikipedia.org/wiki/YAML). `<tmpl_string>` is a Golang [text/template string](https://golang.org/pkg/text/template/) and `<http_config>` has the same fields as [Alertmanager's `http_config`](https://prometheus.io/docs/alerting/latest/configuration/#http_config).

To authenticate to a standard Matrix server, specify an [access token](https://matrix.org/docs/guides/client-server-api#login) as the bearer token in `matrix_http_config`.

```yml
homeserver_url: <string>
text: <tmpl_string>
matrix_http_config: <http_config>
```

Alertmanager Matrix can reload its configuration file at runtime. If the new configuration is not well-formed, the changes will not be applied.
A configuration reload is triggered by sending a `SIGHUP` to the process or by sending a HTTP POST request to the `/-/reload` endpoint.

To view all available command-line flags, run `./alertmanager_matrix -h`.

To specify which configuration file to load, use the `--config.file` flag.

Additionally, an [example configuration](ammatrix.example.yml) is also available.

### Template data

The `text` template is executed with an `Alerts` struct parsed from [Alertmanager's webhook JSON format](https://prometheus.io/docs/alerting/latest/configuration/#webhook_config).

```go
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
```

## Alertmanager Configuration

This webhook receiver needs to be configured as an Alertmanager [`webhook_config`](https://prometheus.io/docs/alerting/latest/configuration/#webhook_config). A `room_id` query parameter must be supplied. Only raw room IDs are supported (must start with a `!`), **aliases will not work**.

To send routes to different rooms, define multiple receivers with different `room_id`s.

```yml
...

receivers:
- name: 'matrix'
  webhook_configs:
    - url: http://127.0.0.1:9751?room_id=!YiweMkCfAD:matrix.org

...
```

## Other endpoints

### `/metrics`

Exports Prometheus metrics.

### `/config`

Outputs the current YAML config, with secrets redacted.

### `/-/reload`

POST requests to this endpoint trigger a configuration reload.

### `/-/healthy` and `/-/ready`

Always respond with an HTTP 200.
