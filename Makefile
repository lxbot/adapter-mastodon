.PHONY: build

build:
	go build -buildmode=plugin -o adapter-mastodon.so adapter.go
