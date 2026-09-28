.PHONY: amd arm build clean

amd:
	$(MAKE) build ARCH=amd64
arm:
	$(MAKE) build ARCH=arm64

GO_ENV = CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH)

build:
	@echo "==> golang编译 linux-$(ARCH)..."
	@cd app/kclient && $(GO_ENV) go build -o server ./

	@echo "==> 正在打包 fpk..."
	@fnpack build
	@mv webtop.fpk Webtop-$(ARCH).fpk

	@rm -f /vol1/1000/Webtop-*.fpk
	@cp Webtop-$(ARCH).fpk /vol1/1000/
	@make clean

clean:
	@echo "==> 清理编译文件..."
	@rm -f app/kclient/server
	@rm -f Webtop-*.fpk