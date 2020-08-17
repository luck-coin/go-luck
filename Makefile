# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: luck android ios luck-cross evm all test clean
.PHONY: luck-linux luck-linux-386 luck-linux-amd64 luck-linux-mips64 luck-linux-mips64le
.PHONY: luck-linux-arm luck-linux-arm-5 luck-linux-arm-6 luck-linux-arm-7 luck-linux-arm64
.PHONY: luck-darwin luck-darwin-386 luck-darwin-amd64
.PHONY: luck-windows luck-windows-386 luck-windows-amd64

GOBIN = ./build/bin
GO ?= latest
GORUN = env GO111MODULE=on go run

luck:
	$(GORUN) build/ci.go install ./cmd/luck
	@echo "Done building."
	@echo "Run \"$(GOBIN)/luck\" to launch luck."

all:
	$(GORUN) build/ci.go install

android:
	$(GORUN) build/ci.go aar --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/luck.aar\" to use the library."

ios:
	$(GORUN) build/ci.go xcode --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/Geth.framework\" to use the library."

test: all
	$(GORUN) build/ci.go test

lint: ## Run linters.
	$(GORUN) build/ci.go lint

clean:
	env GO111MODULE=on go clean -cache
	rm -fr build/_workspace/pkg/ $(GOBIN)/*

# The devtools target installs tools required for 'go generate'.
# You need to put $GOBIN (or $GOPATH/bin) in your PATH to use 'go generate'.

devtools:
	env GOBIN= go get -u golang.org/x/tools/cmd/stringer
	env GOBIN= go get -u github.com/kevinburke/go-bindata/go-bindata
	env GOBIN= go get -u github.com/fjl/gencodec
	env GOBIN= go get -u github.com/golang/protobuf/protoc-gen-go
	env GOBIN= go install ./cmd/abigen
	@type "npm" 2> /dev/null || echo 'Please install node.js and npm'
	@type "solc" 2> /dev/null || echo 'Please install solc'
	@type "protoc" 2> /dev/null || echo 'Please install protoc'

# Cross Compilation Targets (xgo)

luck-cross: luck-linux luck-darwin luck-windows luck-android luck-ios
	@echo "Full cross compilation done:"
	@ls -ld $(GOBIN)/luck-*

luck-linux: luck-linux-386 luck-linux-amd64 luck-linux-arm luck-linux-mips64 luck-linux-mips64le
	@echo "Linux cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-*

luck-linux-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/386 -v ./cmd/luck
	@echo "Linux 386 cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-* | grep 386

luck-linux-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/amd64 -v ./cmd/luck
	@echo "Linux amd64 cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-* | grep amd64

luck-linux-arm: luck-linux-arm-5 luck-linux-arm-6 luck-linux-arm-7 luck-linux-arm64
	@echo "Linux ARM cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-* | grep arm

luck-linux-arm-5:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-5 -v ./cmd/luck
	@echo "Linux ARMv5 cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-* | grep arm-5

luck-linux-arm-6:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-6 -v ./cmd/luck
	@echo "Linux ARMv6 cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-* | grep arm-6

luck-linux-arm-7:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-7 -v ./cmd/luck
	@echo "Linux ARMv7 cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-* | grep arm-7

luck-linux-arm64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm64 -v ./cmd/luck
	@echo "Linux ARM64 cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-* | grep arm64

luck-linux-mips:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips --ldflags '-extldflags "-static"' -v ./cmd/luck
	@echo "Linux MIPS cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-* | grep mips

luck-linux-mipsle:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mipsle --ldflags '-extldflags "-static"' -v ./cmd/luck
	@echo "Linux MIPSle cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-* | grep mipsle

luck-linux-mips64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips64 --ldflags '-extldflags "-static"' -v ./cmd/luck
	@echo "Linux MIPS64 cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-* | grep mips64

luck-linux-mips64le:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips64le --ldflags '-extldflags "-static"' -v ./cmd/luck
	@echo "Linux MIPS64le cross compilation done:"
	@ls -ld $(GOBIN)/luck-linux-* | grep mips64le

luck-darwin: luck-darwin-386 luck-darwin-amd64
	@echo "Darwin cross compilation done:"
	@ls -ld $(GOBIN)/luck-darwin-*

luck-darwin-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=darwin/386 -v ./cmd/luck
	@echo "Darwin 386 cross compilation done:"
	@ls -ld $(GOBIN)/luck-darwin-* | grep 386

luck-darwin-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=darwin/amd64 -v ./cmd/luck
	@echo "Darwin amd64 cross compilation done:"
	@ls -ld $(GOBIN)/luck-darwin-* | grep amd64

luck-windows: luck-windows-386 luck-windows-amd64
	@echo "Windows cross compilation done:"
	@ls -ld $(GOBIN)/luck-windows-*

luck-windows-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=windows/386 -v ./cmd/luck
	@echo "Windows 386 cross compilation done:"
	@ls -ld $(GOBIN)/luck-windows-* | grep 386

luck-windows-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=windows/amd64 -v ./cmd/luck
	@echo "Windows amd64 cross compilation done:"
	@ls -ld $(GOBIN)/luck-windows-* | grep amd64
