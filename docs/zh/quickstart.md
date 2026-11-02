# 快速开始

## 前置条件

- Go 1.26+
- `make` 与 C 工具链（用于 `golangci-lint`）

## 构建与测试

```sh
make ci        # gofmt 检查 + vet + 构建 + 测试
make test-race
make lint      # golangci-lint
```

`make ci` 依次执行 gofmt 检查、`go vet`、构建与完整测试套件。CI 在 Go 1.26.x 与
1.27.x 上执行同样检查，另含 golangci-lint 与依赖漏洞扫描。

## 最小运行时

最小示例只接线一个带模块注册表、分层配置与优雅关闭的运行时，不需要任何链服务：

```sh
go run ./examples/minimal
```

签名示例演示测试签名器、审计钩子、allow-list 拒绝与生成式开发密钥（仓库不附带任何密钥）：

```sh
go run ./examples/signing
```

事件流示例用进程内事件源对接内存存储与 Dispatcher，SIGTERM 干净关停：

```sh
go run ./examples/eventstream
```

## 下一步

- 读[规格](https://github.com/cuihairu/chame/blob/main/docs/design/chame-spec.md)了解完整架构。
- 读[配置 / API](/zh/config)了解配置键与入口。
- `todo.md` 为分期实施总账。
