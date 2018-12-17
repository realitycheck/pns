# PMS Makefile
GO ?= go
DEP ?= dep
DOCKER ?= docker
COMPOSE ?= docker-compose

GO_PACKAGE ?= $(shell echo ${PWD} | sed -e "s/.*src\///")
GO_PROJECT ?= $(notdir ${GO_PACKAGE})

GO_APP_FILE ?= ${GO_PROJECT}
GO_OUTPUT_FILE ?= ${GO_PROJECT}

GO_APP_CONSUMER ?= consumer
GO_APP_PRODUCER ?= producer

COMPOSE_PROJECT_NAME ?= ${GO_PROJECT}
COMPOSE_APP_FILE ?= configs/app.yaml
COMPOSE_METRICS_FILE ?= configs/metrics.yaml
COMPOSE_TEST_FILE ?= configs/test.yaml

COMMIT = $(shell git rev-parse HEAD)
VERSION = $(shell git describe --tags --exact-match --always)
DATE = $(shell date +'%FT%TZ%z')

UID = $(shell id -u)

.SHELLFLAGS = -c # Run commands in a -c flag
.PHONY: build clean app metrics test all

${GO_APP_FILE}: vendor
	go build -o ${GO_OUTPUT_FILE} -a \
	-ldflags '-s -w -extldflags "-static" -X main.name=$(GO_APP_FILE) -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)' \
	./cmd/${GO_APP_FILE}

vendor:
	$(GO) get -u github.com/golang/dep/cmd/dep
	$(DEP) ensure -vendor-only

build: ${GO_APP_FILE}

clean:
	rm -f ${GO_APP_FILE}

app:
	DATA_USER=$(UID) \
	GO_PACKAGE=${GO_PACKAGE} \
	GO_OUTPUT_FILE=$(GO_PROJECT) \
	GO_APP_FILE=$(GO_APP_FILE) \
	$(COMPOSE) -f ${COMPOSE_APP_FILE} up --build

metrics:
	DATA_USER=$(UID) \
	$(COMPOSE) -f ${COMPOSE_METRICS_FILE} up

test:
	DATA_USER=$(UID) \
	GO_PACKAGE=${GO_PACKAGE} \
	GO_APP_CONSUMER=$(GO_APP_CONSUMER) \
	GO_APP_PRODUCER=$(GO_APP_PRODUCER) \
	$(COMPOSE) -f ${COMPOSE_TEST_FILE} up --build

all: app metrics test