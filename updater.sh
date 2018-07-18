#!/bin/bash
echo "$1"
git tag "$1" && git push --tags
go get -u "github.com/ad/gocc/..."
go-bindata -nometadata -pkg bindata -prefix templates/ -o bindata/bindata.go templates/*
gox -output="release/{{.Dir}}_{{.OS}}_{{.Arch}}" -osarch="darwin/386 darwin/amd64 linux/386 linux/amd64 linux/arm"
github-release release --user ad --repo gocc --tag "$1" --name "$1"
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_darwin_386" --file release/gocc_darwin_386
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_darwin_amd64" --file release/gocc_darwin_amd64
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_linux_386" --file release/gocc_linux_386
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_linux_amd64" --file release/gocc_linux_amd64
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_linux_arm" --file release/gocc_linux_arm
