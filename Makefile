# PMS Makefile
GO ?= go
DEP ?= dep
DOCKER ?= docker
COMPOSE ?= docker-compose

GO_PACKAGE ?= $(shell echo ${PWD} | sed -e "s/.*src\///")
GO_OUTPUT_FILE ?= $(notdir ${GO_PACKAGE})
DOCKER_TAG ?= ${GO_OUTPUT_FILE}
COMPOSE_APP_FILE ?= configs/app.yaml
COMPOSE_METRICS_FILE ?= configs/metrics.yaml

COMMIT = $(shell git rev-parse HEAD)
VERSION = $(shell git describe --tags --exact-match --always)
DATE = $(shell date +'%FT%TZ%z')

UID = $(shell id -u)

.SHELLFLAGS = -c # Run commands in a -c flag
.PHONY: build clean up

${GO_OUTPUT_FILE}: vendor
	go build -o ${GO_OUTPUT_FILE} -a \
	-ldflags '-s -w -extldflags "-static" -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)'

vendor:
	$(GO) get -u github.com/golang/dep/cmd/dep
	$(DEP) ensure -vendor-only

build: ${GO_OUTPUT_FILE}

clean:
	rm -f ${GO_OUTPUT_FILE}

app:
	GO_PACKAGE=${GO_PACKAGE} $(COMPOSE) -p ${GO_OUTPUT_FILE} -f ${COMPOSE_APP_FILE} up --build -d

metrics:
	METRICS_DATA_USER=${UID} $(COMPOSE) -p ${GO_OUTPUT_FILE} -f ${COMPOSE_METRICS_FILE} up -d