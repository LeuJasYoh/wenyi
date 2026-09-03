# wenyi-multi 三组件构建/测试（对齐任务书 §5 与架构分册 §5）
WORKSPACE := $(CURDIR)
SPEC := D:/Projects/wenyi-spec-export

.PHONY: build-go build-node build-pdf test-go test-node test-pdf test build clean

build-go:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/wenyi.exe ./cmd/wenyi

build-node:
	cd node && npm ci --no-audit --no-fund || npm install --no-audit --no-fund

build-pdf:
	cd pdf/Wenyi.Pdf && dotnet publish -c Release --no-self-contained -o $(WORKSPACE)/dist/wenyi-pdf

build: build-go build-node build-pdf

test-go:
	go test ./...

test-node:
	cd node && node --test

test-pdf:
	cd pdf/Wenyi.Pdf.Tests && dotnet test -v q

test: test-go test-node test-pdf

fixtures:
	cd node && node scripts/gen-fixtures.mjs

build-ui:
	cd node/ui && npm install && npm run build

clean:
	rm -rf dist
