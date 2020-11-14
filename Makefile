.PHONY: build debug

build:
	go build -buildmode=plugin -o adapter-mastodon.so adapter.go

debug:
	go build -gcflags="all=-N -l" -buildmode=plugin -o adapter-mastodon.so adapter.go
