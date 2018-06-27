#!/bin/bash
echo "$1"
git tag "$1" && git push --tags
gox -output="release/{{.Dir}}_{{.OS}}_{{.Arch}}" -osarch="darwin/386 darwin/amd64 linux/386 linux/amd64 linux/arm windows/386 windows/amd64"
github-release release --user ad --repo gocc --tag "$1" --name "$1"
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_darwin_386" --file release/gocc_darwin_386
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_darwin_amd64" --file release/gocc_darwin_amd64
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_linux_386" --file release/gocc_linux_386
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_linux_amd64" --file release/gocc_linux_amd64
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_linux_arm" --file release/gocc_linux_arm
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_windows_386.exe" --file release/gocc_windows_386.exe
github-release upload --user ad --repo gocc --tag "$1" --name "gocc_windows_amd64.exe" --file release/gocc_windows_amd64.exe
