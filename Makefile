NOW=$(shell date "+%Y%m%d%H%M%S")
PWD=$(shell pwd)
BUILD_DIR=$(PWD)/build
BUILD_TIME=$(shell date "+%Y-%m-%dT%H:%M:%S%z")
GIT_COMMIT_ID=$(shell git rev-parse --short HEAD)
LDFLAGS="-w -s -X main.GitCommitId=${GIT_COMMIT_ID} -X main.BuildTime=${BUILD_TIME}"

SRC_DIR=$(PWD)
OBJ_NAME=dynamic-proxy

REMOTE_SERVICE_NAME=dynamic-proxy.service
REMOTE_HOST=dynamic-proxy
REMOTE_HOST_PATH=~/projects/dynamic-proxy
GOARCH=$(shell go env GOARCH)
GOHOSTOS=$(shell go env GOHOSTOS)

.PHONY: default
default: help

help:                              ## Show this help.
	@fgrep -h "##" $(MAKEFILE_LIST) | fgrep -v fgrep | sed -e 's/\\$$//' | sed -e 's/##//'

## clean
.PHONY: clean
clean:
	rm -rf ${BUILD_DIR}

## generate
.PHONY: generate
generate:
	go generate ${SRC_DIR}

.PHONY: mkdir-dir
mkdir-dir:
	mkdir -p ${BUILD_DIR}

## run
.PHONY: run
run: generate
	go run .

## run-once
.PHONY: run-once
run-once: generate
	go run . -once

## run-serve
.PHONY: run-serve
run-serve: generate
	go run . -serve :8080

## run-dev
.PHONY: run-dev
run-dev: generate
	go run -tags=dev -log-level=debug .

## upx
.PHONY: upx
upx:
	upx ${BUILD_DIR}/${OBJ_NAME}

## build
.PHONY: build
build: generate mkdir-dir
	go build -ldflags ${LDFLAGS} -trimpath -o ${BUILD_DIR}/${OBJ_NAME}-${GOHOSTOS}-${GOARCH} ${SRC_DIR}/*.go

## build-linux-amd64
.PHONY: build-linux-amd64
build-linux-amd64: generate mkdir-dir
	GOOS="linux" GOARCH="amd64" go build -ldflags ${LDFLAGS} -trimpath -o ${BUILD_DIR}/${OBJ_NAME}-linux-amd64 ${SRC_DIR}/*.go

## build-linux-arm64
.PHONY: build-linux-arm64
build-linux-arm64: generate mkdir-dir
	GOOS="linux" GOARCH="arm64" go build -ldflags ${LDFLAGS} -trimpath -o ${BUILD_DIR}/${OBJ_NAME}-linux-arm64 ${SRC_DIR}/*.go

## build-darwin-amd64
.PHONY: build-darwin-amd64
build-darwin-amd64: generate mkdir-dir
	GOOS="darwin" GOARCH="amd64" go build -ldflags ${LDFLAGS} -trimpath -o ${BUILD_DIR}/${OBJ_NAME}-darwin-amd64 ${SRC_DIR}/*.go

## build-darwin-arm64
.PHONY: build-darwin-arm64
build-darwin-arm64: generate mkdir-dir
	GOOS="darwin" GOARCH="arm64" go build -ldflags ${LDFLAGS} -trimpath -o ${BUILD_DIR}/${OBJ_NAME}-darwin-arm64 ${SRC_DIR}/*.go

## build-windows-amd64
.PHONY: build-windows-amd64
build-windows-amd64: generate mkdir-dir
	GOOS="windows" GOARCH="amd64" go build -ldflags ${LDFLAGS} -trimpath -o ${BUILD_DIR}/${OBJ_NAME}-windows-amd64.exe ${SRC_DIR}/*.go

## test
.PHONY: test
test:
	go test -v ./...

## test-coverage
.PHONY: test-coverage
test-coverage:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

## vet
.PHONY: vet
vet:
	go vet ./...

## lint
.PHONY: lint
lint:
	golangci-lint run ./...

## tidy
.PHONY: tidy
tidy:
	go mod tidy

## stop-remote-service
.PHONY: stop-remote-service
stop-remote-service:
	ssh ${REMOTE_HOST} "sudo systemctl stop ${REMOTE_SERVICE_NAME}"

## start-remote-service
.PHONY: start-remote-service
start-remote-service:
	ssh ${REMOTE_HOST} "sudo systemctl start ${REMOTE_SERVICE_NAME}"

## restart-remote-service
.PHONY: restart-remote-service
restart-remote-service: stop-remote-service start-remote-service

## deploy-remote-linux-arm64
.PHONY: deploy-remote-linux-arm64
deploy-remote-linux-arm64: build-linux-arm64 stop-remote-service
	scp ${BUILD_DIR}/${OBJ_NAME}-linux-arm64 ${REMOTE_HOST}:${REMOTE_HOST_PATH}/${OBJ_NAME}-linux-arm64
	$(MAKE) start-remote-service

## deploy-remote-linux-amd64
.PHONY: deploy-remote-linux-amd64
deploy-remote-linux-amd64: build-linux-amd64 stop-remote-service
	scp ${BUILD_DIR}/${OBJ_NAME}-linux-amd64 ${REMOTE_HOST}:${REMOTE_HOST_PATH}/${OBJ_NAME}-linux-amd64
	$(MAKE) start-remote-service

## stop-and-copy-to-remote-linux-arm64
.PHONY: stop-and-copy-to-remote-linux-arm64
stop-and-copy-to-remote-linux-arm64: build-linux-arm64 stop-remote-service
	scp ${BUILD_DIR}/${OBJ_NAME}-linux-arm64 ${REMOTE_HOST}:${REMOTE_HOST_PATH}/${OBJ_NAME}-linux-arm64

## stop-and-copy-to-remote-linux-amd64
.PHONY: stop-and-copy-to-remote-linux-amd64
stop-and-copy-to-remote-linux-amd64: build-linux-amd64 stop-remote-service
	scp ${BUILD_DIR}/${OBJ_NAME}-linux-amd64 ${REMOTE_HOST}:${REMOTE_HOST_PATH}/${OBJ_NAME}-linux-amd64

## all
.PHONY: all
all: clean build test
