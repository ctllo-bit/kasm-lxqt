.PHONY: amd arm build clean

amd:
	$(MAKE) build ARCH=amd64
arm:
	$(MAKE) build ARCH=arm64

BIN_DIR = app/bin
GO_ENV = CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH)

build:
	@rm -f Kasmlqxt-*.fpk

	@echo "==> golang编译 linux-$(ARCH)..."
	@cd app/kclient && \
	$(GO_ENV) go build -o ../bin/kasmvnc-client .

	@echo "==> 正在打包 fpk..."
	@fnpack build
	@mv kasm-lxqt.fpk Kasmlqxt-$(ARCH).fpk
	@rm -f $(BIN_DIR)/*
	@rm -f /vol1/1000/Kasmlqxt-*.fpk
	@cp Kasmlqxt-$(ARCH).fpk /vol1/1000/

clean:
	@rm -f $(BIN_DIR)/*
	@rm -f Kasmlqxt-*.fpk