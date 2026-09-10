#!/usr/bin/env python3
"""Report the mac-load-monitor-go LaunchAgent status to Feishu."""

from __future__ import annotations

import argparse
import base64
import hashlib
import hmac
import json
import logging
import os
from pathlib import Path
import re
import shlex
import socket
import subprocess
import sys
import time
from dataclasses import dataclass
from datetime import datetime
from typing import Callable, Dict, Optional, Sequence
from urllib import error, parse, request


DEFAULT_ENV_PATH = Path.home() / "Library/Application Support/mac-load-monitor-go/.env"
DEFAULT_LABEL = "com.local.mac-load-monitor-go"
DEFAULT_RETRY_DELAYS = (1.0, 3.0, 10.0)
RATE_LIMIT_RETRY_DELAYS = (60.0, 180.0, 300.0)
STATE_PATTERN = re.compile(r"^\s*state\s*=\s*(.+?)\s*$", re.MULTILINE)
PID_PATTERN = re.compile(r"^\s*pid\s*=\s*(\d+)\s*$", re.MULTILINE)
RUNS_PATTERN = re.compile(r"^\s*runs\s*=\s*(\d+)\s*$", re.MULTILINE)
LAST_EXIT_PATTERN = re.compile(r"^\s*last exit code\s*=\s*(.+?)\s*$", re.MULTILINE)
LAST_SIGNAL_PATTERN = re.compile(r"^\s*last terminating signal\s*=\s*(.+?)\s*$", re.MULTILINE)
DURATION_PART_PATTERN = re.compile(r"(\d+(?:\.\d+)?)(ms|s|m|h)")


class WatchdogError(RuntimeError):
    pass


class NotificationError(WatchdogError):
    def __init__(
        self,
        message: str,
        retryable: bool = False,
        retry_delays: Sequence[float] = (),
    ) -> None:
        super().__init__(message)
        self.retryable = retryable
        self.retry_delays = tuple(retry_delays)


@dataclass(frozen=True)
class Config:
    webhook_url: str
    webhook_secret: str
    keyword: str
    special_webhook_url: str
    special_webhook_secret: str
    special_keyword: str
    timeout_seconds: float
    label: str = DEFAULT_LABEL

    @classmethod
    def from_file(cls, path: Path) -> "Config":
        file_values = read_env_file(path)

        def value(name: str, default: str = "") -> str:
            return os.environ.get(name, file_values.get(name, default)).strip()

        keyword = value("FEISHU_KEYWORD", "Mac 负载监控")
        if not keyword:
            raise WatchdogError("FEISHU_KEYWORD 不能为空")
        special_keyword = value("SPECIAL_FEISHU_KEYWORD", "Mac 特别告警")
        if not special_keyword:
            raise WatchdogError("SPECIAL_FEISHU_KEYWORD 不能为空")
        timeout = parse_duration_seconds(value("HTTP_TIMEOUT", "10s"))
        return cls(
            webhook_url=value("FEISHU_WEBHOOK_URL"),
            webhook_secret=value("FEISHU_WEBHOOK_SECRET"),
            keyword=keyword,
            special_webhook_url=value("SPECIAL_FEISHU_WEBHOOK_URL"),
            special_webhook_secret=value("SPECIAL_FEISHU_WEBHOOK_SECRET"),
            special_keyword=special_keyword,
            timeout_seconds=timeout,
        )

    def endpoint(self, special: bool) -> tuple[str, str]:
        if special:
            return self.special_webhook_url, self.special_webhook_secret
        return self.webhook_url, self.webhook_secret

    def validate_webhook(self, special: bool = False) -> None:
        webhook_url, _ = self.endpoint(special)
        parsed = parse.urlparse(webhook_url)
        if (
            parsed.scheme != "https"
            or parsed.netloc != "open.feishu.cn"
            or not parsed.path.startswith("/open-apis/bot/v2/hook/")
        ):
            name = "SPECIAL_FEISHU_WEBHOOK_URL" if special else "FEISHU_WEBHOOK_URL"
            raise WatchdogError(f"{name} 必须是飞书自定义机器人的 HTTPS Webhook 地址")


@dataclass(frozen=True)
class ServiceStatus:
    healthy: bool
    state: str
    pid: Optional[int]
    pid_alive: bool
    runs: Optional[int]
    last_exit: str
    detail: str


def read_env_file(path: Path) -> Dict[str, str]:
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise WatchdogError(f"无法读取配置文件 {path}: {exc}") from exc

    values: Dict[str, str] = {}
    for line_number, raw_line in enumerate(lines, 1):
        stripped = raw_line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if stripped.startswith("export "):
            stripped = stripped[7:].lstrip()
        key, separator, raw_value = stripped.partition("=")
        key = key.strip()
        if not separator or not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*", key):
            raise WatchdogError(f"配置文件第 {line_number} 行格式错误")
        try:
            tokens = shlex.split(raw_value, comments=True, posix=True)
        except ValueError as exc:
            raise WatchdogError(f"配置文件第 {line_number} 行引号不完整") from exc
        values[key] = " ".join(tokens) if tokens else ""
    return values


def parse_duration_seconds(raw: str) -> float:
    matches = list(DURATION_PART_PATTERN.finditer(raw))
    if not matches or "".join(match.group(0) for match in matches) != raw:
        raise WatchdogError("HTTP_TIMEOUT 必须是大于 0 的时长，例如 10s")
    multipliers = {"ms": 0.001, "s": 1.0, "m": 60.0, "h": 3600.0}
    seconds = sum(float(match.group(1)) * multipliers[match.group(2)] for match in matches)
    if seconds <= 0:
        raise WatchdogError("HTTP_TIMEOUT 必须大于 0")
    return seconds


def parse_service_output(output: str, pid_checker: Callable[[int], bool]) -> ServiceStatus:
    state_match = STATE_PATTERN.search(output)
    pid_match = PID_PATTERN.search(output)
    runs_match = RUNS_PATTERN.search(output)
    exit_match = LAST_EXIT_PATTERN.search(output)
    signal_match = LAST_SIGNAL_PATTERN.search(output)
    state = state_match.group(1) if state_match else "未知"
    pid = int(pid_match.group(1)) if pid_match else None
    runs = int(runs_match.group(1)) if runs_match else None
    pid_alive = pid is not None and pid_checker(pid)
    last_exit = "无记录"
    if exit_match:
        last_exit = "退出码 " + exit_match.group(1)
    elif signal_match:
        last_exit = "信号 " + signal_match.group(1)
    healthy = state == "running" and pid is not None and pid_alive
    if healthy:
        detail = "LaunchAgent 正在运行，进程存活"
    elif state != "running":
        detail = f"LaunchAgent 状态为 {state}"
    elif pid is None:
        detail = "LaunchAgent 未报告 PID"
    else:
        detail = f"LaunchAgent 报告 PID {pid}，但进程不存在"
    return ServiceStatus(healthy, state, pid, pid_alive, runs, last_exit, detail)


def pid_is_alive(pid: int) -> bool:
    try:
        os.kill(pid, 0)
        return True
    except PermissionError:
        return True
    except ProcessLookupError:
        return False


def check_service(
    label: str,
    timeout_seconds: float,
    run_command: Callable[..., subprocess.CompletedProcess[str]] = subprocess.run,
    pid_checker: Callable[[int], bool] = pid_is_alive,
) -> ServiceStatus:
    domain = f"gui/{os.getuid()}/{label}"
    try:
        completed = run_command(
            ["/bin/launchctl", "print", domain],
            capture_output=True,
            text=True,
            timeout=timeout_seconds,
            check=False,
        )
    except subprocess.TimeoutExpired:
        return ServiceStatus(False, "检查超时", None, False, None, "无记录", "launchctl 检查超时")
    except OSError as exc:
        return ServiceStatus(False, "检查失败", None, False, None, "无记录", f"无法运行 launchctl: {type(exc).__name__}")
    if completed.returncode != 0:
        return ServiceStatus(False, "未加载", None, False, None, "无记录", "LaunchAgent 未安装或未加载")
    return parse_service_output(completed.stdout, pid_checker)


def format_message(config: Config, status: ServiceStatus, now: datetime) -> str:
    conclusion = "正常" if status.healthy else "异常"
    keyword = config.keyword if status.healthy else config.special_keyword
    pid_text = str(status.pid) if status.pid is not None else "无"
    alive_text = "存活" if status.pid_alive else "不存在"
    runs_text = str(status.runs) if status.runs is not None else "未知"
    return "\n".join(
        [
            f"{keyword} | Go 监控服务状态",
            f"主机: {socket.gethostname()}",
            f"时间: {now.astimezone().strftime('%Y-%m-%d %H:%M:%S %z')}",
            f"结论: {conclusion}",
            f"LaunchAgent: {status.state}",
            f"进程: PID {pid_text}，{alive_text}",
            f"启动次数: {runs_text}",
            f"最近退出: {status.last_exit}",
            f"说明: {status.detail}",
        ]
    )


class FeishuNotifier:
    def __init__(
        self,
        config: Config,
        logger: logging.Logger,
        retry_delays: Sequence[float] = DEFAULT_RETRY_DELAYS,
        sleep: Callable[[float], None] = time.sleep,
        clock: Callable[[], float] = time.time,
        opener: Callable[..., object] = request.urlopen,
        special: bool = False,
    ) -> None:
        self.config = config
        self.logger = logger
        self.retry_delays = tuple(retry_delays)
        self.sleep = sleep
        self.clock = clock
        self.opener = opener
        self.webhook_url, self.webhook_secret = config.endpoint(special)

    def build_payload(self, text: str) -> Dict[str, object]:
        payload: Dict[str, object] = {"msg_type": "text", "content": {"text": text}}
        if self.webhook_secret:
            timestamp = str(int(self.clock()))
            signing_key = f"{timestamp}\n{self.webhook_secret}".encode("utf-8")
            digest = hmac.new(signing_key, digestmod=hashlib.sha256).digest()
            payload["timestamp"] = timestamp
            payload["sign"] = base64.b64encode(digest).decode("ascii")
        return payload

    def _post(self, payload: Dict[str, object]) -> None:
        body = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        webhook_request = request.Request(
            self.webhook_url,
            data=body,
            headers={"Content-Type": "application/json; charset=utf-8"},
            method="POST",
        )
        try:
            with self.opener(webhook_request, timeout=self.config.timeout_seconds) as response:
                response_body = response.read(20480)
        except error.HTTPError as exc:
            retryable = exc.code == 429 or exc.code >= 500
            raise NotificationError(f"飞书返回 HTTP {exc.code}", retryable) from exc
        except (error.URLError, TimeoutError, OSError) as exc:
            raise NotificationError(f"飞书网络请求失败: {type(exc).__name__}", True) from exc
        try:
            result = json.loads(response_body.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise NotificationError("飞书返回了无效 JSON") from exc
        code = result.get("code", result.get("StatusCode"))
        try:
            numeric_code = int(code)
        except (TypeError, ValueError) as exc:
            raise NotificationError("飞书响应缺少有效状态码") from exc
        if numeric_code != 0:
            if numeric_code == 11232:
                raise NotificationError(
                    "飞书业务错误码 11232（系统限流）",
                    True,
                    RATE_LIMIT_RETRY_DELAYS,
                )
            raise NotificationError(f"飞书业务错误码 {numeric_code}")

    def send(self, text: str) -> bool:
        payload = self.build_payload(text)
        retry_attempts: Dict[Sequence[float], int] = {}
        while True:
            try:
                self._post(payload)
                return True
            except NotificationError as exc:
                if not exc.retryable:
                    self.logger.error("发送飞书状态失败: %s", exc)
                    return False
                retry_delays = exc.retry_delays or self.retry_delays
                attempt = retry_attempts.get(retry_delays, 0)
                if attempt >= len(retry_delays):
                    self.logger.error("发送飞书状态失败: %s", exc)
                    return False
                delay = retry_delays[attempt]
                retry_attempts[retry_delays] = attempt + 1
                self.logger.warning("发送飞书状态失败，%.0f 秒后重试: %s", delay, exc)
                self.sleep(delay)


def configure_logging() -> logging.Logger:
    logger = logging.getLogger("mac-load-monitor-go-watchdog")
    logger.handlers.clear()
    logger.setLevel(logging.INFO)
    handler = logging.StreamHandler()
    handler.setFormatter(logging.Formatter("%(asctime)s %(levelname)s %(message)s"))
    logger.addHandler(handler)
    return logger


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="检查 mac-load-monitor-go 服务并通过飞书上报")
    parser.add_argument("--env", type=Path, default=DEFAULT_ENV_PATH, help="配置文件路径")
    parser.add_argument("--dry-run", action="store_true", help="只输出状态，不发送飞书")
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    args = build_parser().parse_args(argv)
    logger = configure_logging()
    try:
        config = Config.from_file(args.env)
        status = check_service(config.label, config.timeout_seconds)
        message = format_message(config, status, datetime.now().astimezone())
        if args.dry_run:
            print(message)
            return 0
        special = not status.healthy
        config.validate_webhook(special=special)
        return 0 if FeishuNotifier(config, logger, special=special).send(message) else 1
    except WatchdogError as exc:
        logger.error("watchdog 执行失败: %s", exc)
        return 2
    except KeyboardInterrupt:
        logger.info("watchdog 已中止")
        return 130


if __name__ == "__main__":
    raise SystemExit(main())
