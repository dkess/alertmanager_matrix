.POSIX:

VERSION=0.0.2

PREFIX?=/usr/local
BINDIR?=$(PREFIX)/bin
GO?=go
GOFLAGS?=

GOSRC!=find . -name '*.go'
GOSRC+=go.mod go.sum

alertmanager_matrix: $(GOSRC)
	$(GO) build $(GOFLAGS) \
		-ldflags "-X main.Version=$(VERSION)" \
		-o $@

all: alertmanager_matrix

RM?=rm -f

clean:
	$(RM) alertmanager_matrix

install: all
	mkdir -m755 -p $(DESTDIR)$(BINDIR)
	install -m755 alertmanager_matrix $(DESTDIR)$(BINDIR)/alertmanager_matrix

RMDIR_IF_EMPTY:=sh -c '\
if test -d $$0 && ! ls -1qA $$0 | grep -q . ; then \
	rmdir $$0; \
fi'

uninstall:
	$(RM) $(DESTDIR)$(BINDIR)/alertmanager_matrix
	${RMDIR_IF_EMPTY} $(DESTDIR)$(BINDIR)

.DEFAULT_GOAL := all

.PHONY: all clean install uninstall
