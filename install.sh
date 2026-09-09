#!/bin/zsh
set -euo pipefail

SCRIPT_DIR="${0:A:h}"
SOURCE_ENV="$SCRIPT_DIR/.env"
APP_DIR="$HOME/Library/Application Support/mac-load-monitor-go"
PROGRAM_PATH="$APP_DIR/mac-load-monitor"
WATCHDOG_SOURCE="$SCRIPT_DIR/watchdog.py"
WATCHDOG_PATH="$APP_DIR/watchdog.py"
CONFIG_PATH="$APP_DIR/.env"
LOG_DIR="$HOME/Library/Logs/mac-load-monitor-go"
PLIST_PATH="$HOME/Library/LaunchAgents/com.local.mac-load-monitor-go.plist"
OLD_PLIST_PATH="$HOME/Library/LaunchAgents/com.local.mac-load-monitor.plist"
LABEL="com.local.mac-load-monitor-go"
USER_DOMAIN="gui/$(id -u)"
CRON_BEGIN="# BEGIN mac-load-monitor-go watchdog"
CRON_END="# END mac-load-monitor-go watchdog"

if [[ "$(uname -s)" != "Darwin" ]]; then
  print -u2 "错误：该监控工具仅支持 macOS。"
  exit 2
fi
if [[ ! -f "$SOURCE_ENV" ]]; then
  print -u2 "错误：请先复制 .env.example 为 .env 并填写 FEISHU_WEBHOOK_URL。"
  exit 2
fi

case "$(uname -m)" in
  arm64) GO_ARCH="arm64" ;;
  x86_64) GO_ARCH="amd64" ;;
  *)
    print -u2 "错误：不支持的 Mac 架构 $(uname -m)。"
    exit 2
    ;;
esac

TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/mac-load-monitor-go.XXXXXX")"
trap 'rm -rf "$TEMP_DIR"' EXIT
TEMP_PROGRAM="$TEMP_DIR/mac-load-monitor"
TEMP_PLIST="$TEMP_DIR/com.local.mac-load-monitor-go.plist"

print "正在运行测试并构建原生程序……"
cd "$SCRIPT_DIR"
/usr/bin/env go test ./...
/usr/bin/python3 -m unittest discover -s "$SCRIPT_DIR/tests" -p 'test_*.py'
CGO_ENABLED=0 GOOS=darwin GOARCH="$GO_ARCH" /usr/bin/env go build -trimpath -ldflags="-s -w" -o "$TEMP_PROGRAM" .

print "正在验证飞书推送……"
chmod 600 "$SOURCE_ENV"
"$TEMP_PROGRAM" --env "$SOURCE_ENV" --test-alert
"$TEMP_PROGRAM" --env "$SOURCE_ENV" --test-special-alert

mkdir -p "$APP_DIR" "$LOG_DIR" "${PLIST_PATH:h}"
install -m 755 "$TEMP_PROGRAM" "$PROGRAM_PATH"
install -m 755 "$WATCHDOG_SOURCE" "$WATCHDOG_PATH"
install -m 600 "$SOURCE_ENV" "$CONFIG_PATH"

/usr/bin/plutil -create xml1 "$TEMP_PLIST"
/usr/bin/plutil -insert Label -string "$LABEL" "$TEMP_PLIST"
/usr/bin/plutil -insert ProgramArguments -json '[]' "$TEMP_PLIST"
/usr/bin/plutil -insert ProgramArguments.0 -string "$PROGRAM_PATH" "$TEMP_PLIST"
/usr/bin/plutil -insert ProgramArguments.1 -string "--env" "$TEMP_PLIST"
/usr/bin/plutil -insert ProgramArguments.2 -string "$CONFIG_PATH" "$TEMP_PLIST"
/usr/bin/plutil -insert WorkingDirectory -string "$APP_DIR" "$TEMP_PLIST"
/usr/bin/plutil -insert RunAtLoad -bool true "$TEMP_PLIST"
/usr/bin/plutil -insert KeepAlive -bool true "$TEMP_PLIST"
/usr/bin/plutil -insert ThrottleInterval -integer 10 "$TEMP_PLIST"
/usr/bin/plutil -insert ProcessType -string Background "$TEMP_PLIST"
/usr/bin/plutil -insert StandardOutPath -string /dev/null "$TEMP_PLIST"
/usr/bin/plutil -insert StandardErrorPath -string "$LOG_DIR/monitor.log" "$TEMP_PLIST"
/usr/bin/plutil -lint "$TEMP_PLIST"
install -m 644 "$TEMP_PLIST" "$PLIST_PATH"

/bin/launchctl bootout "$USER_DOMAIN" "$PLIST_PATH" 2>/dev/null || true
if [[ -f "$OLD_PLIST_PATH" ]]; then
  /bin/launchctl bootout "$USER_DOMAIN" "$OLD_PLIST_PATH" 2>/dev/null || true
fi
/bin/launchctl bootstrap "$USER_DOMAIN" "$PLIST_PATH"
/bin/launchctl enable "$USER_DOMAIN/$LABEL"
/bin/launchctl kickstart -k "$USER_DOMAIN/$LABEL"

CURRENT_CRONTAB="$TEMP_DIR/crontab.current"
UPDATED_CRONTAB="$TEMP_DIR/crontab.updated"
/usr/bin/crontab -l > "$CURRENT_CRONTAB" 2>/dev/null || true
/usr/bin/awk -v begin="$CRON_BEGIN" -v end="$CRON_END" '
  $0 == begin { skipping = 1; next }
  $0 == end { skipping = 0; next }
  !skipping { print }
' "$CURRENT_CRONTAB" > "$UPDATED_CRONTAB"
{
  print -r -- "$CRON_BEGIN"
  print -r -- "5 * * * * /usr/bin/python3 \"$WATCHDOG_PATH\" --env \"$CONFIG_PATH\" >> \"$LOG_DIR/watchdog.log\" 2>&1"
  print -r -- "$CRON_END"
} >> "$UPDATED_CRONTAB"
/usr/bin/crontab "$UPDATED_CRONTAB"

print "安装完成。Go 监控已启动，旧 Python 服务已停止但文件仍保留。"
print "状态：launchctl print '$USER_DOMAIN/$LABEL'"
print "日志：tail -f '$LOG_DIR/monitor.log'"
print "存活上报：每小时第 5 分钟执行，日志位于 '$LOG_DIR/watchdog.log'"
