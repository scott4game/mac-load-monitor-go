# mac-load-monitor-go

macOS 硬件负载监控。程序启动后立即检查一次，之后每个整 10 分钟检测过载，并在每个整点通过飞书群自定义机器人推送完整状态。

需要 macOS 和 Go 1.24 或更高版本；安装后运行监控不再依赖 Go 工具链。

默认告警线：

- CPU 使用率达到 `90%`
- macOS 内存压力可用评分低于 `20%`
- 根文件系统 `/` 使用率达到 `90%`
- macOS 报告影响 CPU 性能的温度压力

正常的每小时状态和恢复通知发送到普通飞书 Webhook。硬件超负荷属于特别告警，发送到独立的特别告警 Webhook；异常期间每次检测都会推送。磁盘吞吐按两次采样间的累计计数计算平均读写速率与 IOPS，只做状态报告，不默认触发告警。

项目还包含一个 Python watchdog。它通过 `launchctl` 检查 Go LaunchAgent 和对应 PID，每小时第 5 分钟由当前用户的 `crontab` 执行一次。服务正常时发送到普通 Webhook；服务异常时只发送到特别告警 Webhook。

## 配置

首次使用先创建被 Git 忽略的 `.env`，再填写飞书 Webhook：

```bash
cp .env.example .env
chmod 600 .env
```

```dotenv
FEISHU_WEBHOOK_URL=https://open.feishu.cn/open-apis/bot/v2/hook/REPLACE_ME
FEISHU_WEBHOOK_SECRET=
FEISHU_KEYWORD=Mac 负载监控

SPECIAL_FEISHU_WEBHOOK_URL=https://open.feishu.cn/open-apis/bot/v2/hook/REPLACE_SPECIAL
SPECIAL_FEISHU_WEBHOOK_SECRET=
SPECIAL_FEISHU_KEYWORD=Mac 特别告警
```

普通机器人和特别告警机器人可以分别启用签名校验。对应密钥填写到 `FEISHU_WEBHOOK_SECRET` 和 `SPECIAL_FEISHU_WEBHOOK_SECRET`。其余周期和阈值可参考 `.env.example` 修改，时长采用 Go 格式，例如 `10m`、`1h`、`30s`。

进程排行只包含 PID、程序名和占用，不发送完整命令行或参数。Webhook 与密钥不会写入日志。

飞书返回业务错误码 `11232`（系统限流）时，Go 监控和 Python watchdog 都会再重试 3 次，间隔依次为 1 分钟、3 分钟和 5 分钟。其他可重试网络错误继续使用秒级退避。

## 使用

```bash
# 查看一次当前状态，不发送消息；Webhook 可以暂时留空
go run . --env .env --once

# 发送飞书测试消息
go run . --env .env --test-alert

# 发送特别告警测试消息
go run . --env .env --test-special-alert

# 前台运行常驻监控
go run . --env .env

# 检查 Go 服务状态，只打印而不推送
/usr/bin/python3 watchdog.py --env .env --dry-run

# 检查 Go 服务状态并推送飞书
/usr/bin/python3 watchdog.py --env .env
```

## 安装为登录项

填写 `.env` 后执行：

```bash
chmod +x install.sh
./install.sh
```

安装程序会运行 Go 与 Python 测试、构建当前 Mac 原生架构程序，并分别向普通和特别告警 Webhook 发送测试消息，然后安装并启动 `com.local.mac-load-monitor-go` LaunchAgent。它还会幂等更新 watchdog 的 cron 区块，不会覆盖已有的其他 cron 任务。现有旧版 Python 负载监控服务会被停止，但源码和配置不会删除。

## 更新与重启

更新代码后重新运行安装脚本即可。脚本会重新测试和构建、覆盖已安装的 Go 程序与 watchdog、更新 cron，并重启 LaunchAgent：

```bash
cd /Users/scottzh/workspace_starwar_proj/tools/mac-load-monitor-go
git pull --ff-only
./install.sh
```

项目目录中的 `.env` 是配置源；修改 Webhook、周期或阈值后，也应重新运行 `./install.sh`，将配置复制到安装目录并重启服务。

只重启 Go 监控、不更新程序和配置时执行：

```bash
launchctl kickstart -k "gui/$(id -u)/com.local.mac-load-monitor-go"
```

Python watchdog 不是常驻进程，不需要重启。它由 cron 每小时启动一次；重新运行 `./install.sh` 会更新脚本和 cron。也可以用后文的命令立即手动执行一次。

安装路径：

- 程序与私密配置：`~/Library/Application Support/mac-load-monitor-go`
- 登录项：`~/Library/LaunchAgents/com.local.mac-load-monitor-go.plist`
- 负载监控日志：`~/Library/Logs/mac-load-monitor-go/monitor.log`
- 存活上报日志：`~/Library/Logs/mac-load-monitor-go/watchdog.log`

```bash
# 查看服务状态
launchctl print "gui/$(id -u)/com.local.mac-load-monitor-go"

# 重启服务
launchctl kickstart -k "gui/$(id -u)/com.local.mac-load-monitor-go"

# 查看日志
tail -f "$HOME/Library/Logs/mac-load-monitor-go/monitor.log"

# 查看每小时存活上报任务
crontab -l

# 立即执行一次存活检查并推送
/usr/bin/python3 "$HOME/Library/Application Support/mac-load-monitor-go/watchdog.py" \
  --env "$HOME/Library/Application Support/mac-load-monitor-go/.env"

# 停止服务
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/com.local.mac-load-monitor-go.plist"
```

移除 watchdog 定时任务时运行 `crontab -e`，删除 `# BEGIN mac-load-monitor-go watchdog` 到 `# END mac-load-monitor-go watchdog` 之间的三行。

电脑睡眠或关机时无法采样和推送。Go 监控唤醒后只补充一次当前状态，不重放睡眠期间错过的消息；cron 也不会补发睡眠期间错过的小时状态。
