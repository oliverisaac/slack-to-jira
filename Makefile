.DEFAULT_GOAL := build

PWD=$(shell pwd )
NAME=$(shell basename "${PWD}" )
IMAGE=oliverisaac/${NAME}
# Gets the current tag number from docker hub and increments by 1
TAG=$(shell bash -euo pipefail -c 'curl -s -L https://registry.hub.docker.com/v1/repositories/${IMAGE}/tags | jq -r ".[].name" | grep "^v" | sort -V | tail -n 1 | tr "." " " | awk "{\$$NF = \$$NF + 1; print;}" | tr " " "."')

.PHONY: build
build: clean go-mod go-test
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -a -o bin/${NAME}-darwin-amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o bin/${NAME}-linux-amd64 .

.PHONY: clean
clean:
	rm bin/${NAME}-* || true

.PHONY: go-mod
go-mod:
	go mod tidy

.PHONY: go-test
go-test:
	go test ./...

.PHONY: docker-build
docker-build:  go-mod go-test
	docker build --build-arg APP_NAME=${NAME} -t ${IMAGE}:latest .
	docker tag "${IMAGE}:latest" "${IMAGE}:${TAG}" 
	docker build --build-arg APP_NAME=${NAME} -t ${IMAGE}:alpine-latest -f Dockerfile.alpine .
	docker tag "${IMAGE}:alpine-latest" "${IMAGE}:alpine-${TAG}" 

.PHONY: docker-push
docker-push:  docker-build 
	docker push ${IMAGE}:${TAG}
	docker push ${IMAGE}:latest
	docker push ${IMAGE}:alpine-${TAG}
	docker push ${IMAGE}:alpine-latest
