# PMS Makefile
GO ?= go
DEP ?= dep
DOCKER ?= docker
COMPOSE ?= docker-compose

GO_PACKAGE ?= $(shell echo ${PWD} | sed -e "s/.*src\///")
GO_OUTPUT_FILE ?= $(notdir ${GO_PACKAGE})
DOCKER_TAG ?= ${GO_OUTPUT_FILE}
COMPOSE_FILE ?= configs/docker-compose.yaml

COMMIT=$(shell git rev-parse HEAD)
VERSION=$(shell git describe --tags --exact-match --always)
DATE=$(shell date +'%FT%TZ%z')

.SHELLFLAGS = -c # Run commands in a -c flag
.PHONY: build clean up

${GO_OUTPUT_FILE}: vendor
	go build -o ${GO_OUTPUT_FILE} -a .

vendor:
	$(GO) get -u github.com/golang/dep/cmd/dep
	$(DEP) ensure -vendor-only

build: ${GO_OUTPUT_FILE}

clean:
	rm -f ${GO_OUTPUT_FILE}

up:
	GO_PACKAGE=${GO_PACKAGE} $(COMPOSE) -p ${GO_OUTPUT_FILE} -f ${COMPOSE_FILE} up --build

info:
	echo ${COMMIT}
	echo ${VERSION}
	echo ${DATE}