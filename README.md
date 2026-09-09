# mac-load-monitor-go

macOS 硬件负载监控。程序启动后立即检查一次，之后每个整 10 分钟检测过载，并在每个整点通过飞书群自定义机器人推送完整状态。

需要 macOS 和 Go 1.24 或更高版本；安装后运行监控不再依赖 Go 工具链。

默认告警线：

- CPU 使用率达到 `90%`
- macOS 内存压力可用评分低于 `20%`
- 根文件系统 `/` 使用率达到 `90%`
- macOS 报告影响 CPU 性能的温度压力

异常期间每次检测都会推送；恢复后的第一次正常检测会推送恢复通知。磁盘吞吐按两次采样间的累计计数计算平均读写速率与 IOPS，只做状态报告，不默认触发告警。

## 配置

项目已经提供被 Git 忽略的 `.env`。填写飞书 Webhook：

```dotenv
FEISHU_WEBHOOK_URL=https://open.feishu.cn/open-apis/bot/v2/hook/REPLACE_ME
FEISHU_WEBHOOK_SECRET=
FEISHU_KEYWORD=Mac 负载监控
```

如果机器人启用了签名校验，将密钥填入 `FEISHU_WEBHOOK_SECRET`。其余周期和阈值可参考 `.env.example` 修改，时长采用 Go 格式，例如 `10m`、`1h`、`30s`。

进程排行只包含 PID、程序名和占用，不发送完整命令行或参数。Webhook 与密钥不会写入日志。

## 使用

```bash
# 查看一次当前状态，不发送消息；Webhook 可以暂时留空
go run . --env .env --once

# 发送飞书测试消息
go run . --env .env --test-alert

# 前台运行常驻监控
go run . --env .env
```

## 安装为登录项

填写 `.env` 后执行：

```bash
chmod +x install.sh
./install.sh
```

安装程序会运行测试、构建当前 Mac 原生架构程序、发送一条飞书测试消息，然后安装并启动 `com.local.mac-load-monitor-go` LaunchAgent。现有 Python 监控服务会被停止，但源码和配置不会删除。

安装路径：

- 程序与私密配置：`~/Library/Application Support/mac-load-monitor-go`
- 登录项：`~/Library/LaunchAgents/com.local.mac-load-monitor-go.plist`
- 日志：`~/Library/Logs/mac-load-monitor-go/monitor.log`

```bash
# 查看服务状态
launchctl print "gui/$(id -u)/com.local.mac-load-monitor-go"

# 重启服务
launchctl kickstart -k "gui/$(id -u)/com.local.mac-load-monitor-go"

# 查看日志
tail -f "$HOME/Library/Logs/mac-load-monitor-go/monitor.log"

# 停止服务
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.local.mac-load-monitor-go.plist"
```

电脑睡眠或关机时无法采样和推送。唤醒后程序只补充一次当前状态，不重放睡眠期间错过的消息。
