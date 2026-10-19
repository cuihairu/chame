# ADR-0007: 可观测性以 slog 门面 + 接口先行

- 状态：已接受（代决，用户可推翻）
- 日期：2026-10-15
- 对应规格：§15、§23 第 7 项

## 背景

§23 要求决定可观测性是只提供抽象，还是同时提供官方指标导出器。

## 决策

- **日志**：以标准库 `log/slog` 为门面；`core/logging` 只定义窄接口（注入 logger，禁全局 logger）。敏感配置不落日志（§12）。
- **指标**：核心定义窄指标契约；生产基础设施提供标准库 Prometheus text exposition handler，不引入 Prometheus client 或 collector 依赖。
- **健康检查**：`core/health` 的模型随 Phase 1 落地。

即：抽象与零第三方依赖的标准格式导出器分离；外部采集和告警由部署方配置。

## 理由

- 框架不应绑定监控栈；窄接口让游戏项目自带导出器可行。
- `slog` 是 Go 标准，无需引入第三方日志库，也便于宿主项目桥接自己的日志。
- Prometheus text 可由 Prometheus 直接 scrape，且无需额外 client 库。

## 备选方案

- **引入 Prometheus client_golang**：否。当前 exposition 仅需标准库 HTTP 与内存快照，避免重依赖。
- **采用 OTLP**：否。需要部署 collector/export pipeline 与配置；宿主已有该基础设施时可后续桥接。

## 影响范围

- 模块与适配器只依赖 `core/logging` 的接口类型，不直接 import 监控库。
- 指标命名及低基数标签约束由 `core/metrics` 和 Prometheus recorder 定义。

## 测试要求

- 敏感字段脱敏的日志测试（密钥/助记词不出现在输出）。
- 导出器模块交付时：接口契约测试 + 与接口实现的一致性测试。
