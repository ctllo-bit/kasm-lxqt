.PHONY: amd arm build clean

amd:
	$(MAKE) build ARCH=amd64
arm:
	$(MAKE) build ARCH=arm64

GO_ENV = CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH)

build:
	@rm -f Kasmlqxt-*.fpk

	@echo "==> golang编译 linux-$(ARCH)..."
	@cd app/kclient && \
	$(GO_ENV) go build -o ../server/kasmvnc-client .

	@echo "==> 正在打包 fpk..."
	@fnpack build
	@mv kasm-lxqt.fpk Kasmlqxt-$(ARCH).fpk
	@rm -f app/server/kasmvnc-client
	@rm -f /vol1/1000/Kasmlqxt-*.fpk
	@cp Kasmlqxt-$(ARCH).fpk /vol1/1000/

clean:
	@rm -f app/server/kasmvnc-client
	@rm -f Kasmlqxt-*.fpk