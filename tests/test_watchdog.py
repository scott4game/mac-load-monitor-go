import base64
import hashlib
import hmac
import io
import json
import logging
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import watchdog


WEBHOOK_URL = "https://open.feishu.cn/open-apis/bot/v2/hook/test-id"


def make_config(**overrides):
    values = {
        "webhook_url": WEBHOOK_URL,
        "webhook_secret": "",
        "keyword": "Mac 负载监控",
        "timeout_seconds": 10.0,
    }
    values.update(overrides)
    return watchdog.Config(**values)


class FakeResponse:
    def __init__(self, body):
        self.body = body

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_value, traceback):
        return False

    def read(self, size):
        return self.body[:size]


class ConfigTests(unittest.TestCase):
    def test_reads_shared_env_format(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / ".env"
            path.write_text(
                "FEISHU_WEBHOOK_URL=https://open.feishu.cn/open-apis/bot/v2/hook/id\n"
                "FEISHU_WEBHOOK_SECRET='secret # value'\n"
                "HTTP_TIMEOUT=1m30s\n",
                encoding="utf-8",
            )
            config = watchdog.Config.from_file(path)
        self.assertEqual(config.webhook_secret, "secret # value")
        self.assertEqual(config.timeout_seconds, 90)

    def test_rejects_non_feishu_webhook(self):
        with self.assertRaises(watchdog.WatchdogError):
            make_config(webhook_url="https://example.com/hook").validate_webhook()


class ServiceStatusTests(unittest.TestCase):
    RUNNING_OUTPUT = """
    state = running
    runs = 4
    pid = 12345
    last exit code = 0
    """

    def test_running_service_with_live_pid_is_healthy(self):
        status = watchdog.parse_service_output(self.RUNNING_OUTPUT, lambda pid: pid == 12345)
        self.assertTrue(status.healthy)
        self.assertEqual(status.runs, 4)
        self.assertEqual(status.last_exit, "退出码 0")

    def test_running_service_with_stale_pid_is_unhealthy(self):
        status = watchdog.parse_service_output(self.RUNNING_OUTPUT, lambda pid: False)
        self.assertFalse(status.healthy)
        self.assertIn("进程不存在", status.detail)

    def test_waiting_service_is_unhealthy(self):
        status = watchdog.parse_service_output("state = waiting\nruns = 2\n", lambda pid: True)
        self.assertFalse(status.healthy)
        self.assertEqual(status.state, "waiting")

    def test_missing_service_is_unhealthy(self):
        completed = subprocess.CompletedProcess([], 113, "", "not found")
        status = watchdog.check_service("label", 1, run_command=lambda *args, **kwargs: completed)
        self.assertFalse(status.healthy)
        self.assertEqual(status.state, "未加载")

    def test_launchctl_timeout_is_unhealthy(self):
        def timeout(*args, **kwargs):
            raise subprocess.TimeoutExpired("launchctl", 1)

        status = watchdog.check_service("label", 1, run_command=timeout)
        self.assertFalse(status.healthy)
        self.assertEqual(status.state, "检查超时")


class FeishuNotifierTests(unittest.TestCase):
    def setUp(self):
        self.logs = io.StringIO()
        self.logger = logging.getLogger(self.id())
        self.logger.handlers = [logging.StreamHandler(self.logs)]
        self.logger.setLevel(logging.DEBUG)

    def test_signature_payload(self):
        notifier = watchdog.FeishuNotifier(
            make_config(webhook_secret="secret"), self.logger, clock=lambda: 1700000000
        )
        payload = notifier.build_payload("hello")
        key = b"1700000000\nsecret"
        expected = base64.b64encode(hmac.new(key, digestmod=hashlib.sha256).digest()).decode("ascii")
        self.assertEqual(payload["timestamp"], "1700000000")
        self.assertEqual(payload["sign"], expected)

    def test_accepts_current_and_legacy_success(self):
        responses = iter(
            [
                FakeResponse(json.dumps({"code": 0}).encode()),
                FakeResponse(json.dumps({"StatusCode": 0}).encode()),
            ]
        )
        notifier = watchdog.FeishuNotifier(
            make_config(), self.logger, retry_delays=(), opener=lambda *args, **kwargs: next(responses)
        )
        self.assertTrue(notifier.send("one"))
        self.assertTrue(notifier.send("two"))

    def test_retries_network_failure_then_succeeds(self):
        delays = []
        notifier = watchdog.FeishuNotifier(
            make_config(), self.logger, retry_delays=(1, 3), sleep=delays.append
        )
        notifier._post = mock.Mock(
            side_effect=[
                watchdog.NotificationError("temporary", True),
                watchdog.NotificationError("temporary", True),
                None,
            ]
        )
        self.assertTrue(notifier.send("hello"))
        self.assertEqual(delays, [1, 3])

    def test_business_error_does_not_retry_or_leak_webhook(self):
        secret_url = "https://open.feishu.cn/open-apis/bot/v2/hook/very-secret-token"
        notifier = watchdog.FeishuNotifier(
            make_config(webhook_url=secret_url), self.logger, retry_delays=()
        )
        notifier._post = mock.Mock(side_effect=watchdog.NotificationError("business error"))
        self.assertFalse(notifier.send("hello"))
        self.assertEqual(notifier._post.call_count, 1)
        self.assertNotIn(secret_url, self.logs.getvalue())
        self.assertNotIn("very-secret-token", self.logs.getvalue())

    def test_http_500_is_retried(self):
        http_error = watchdog.error.HTTPError(WEBHOOK_URL, 500, "error", {}, None)
        responses = iter([http_error, FakeResponse(b'{"code": 0}')])

        def opener(*args, **kwargs):
            response = next(responses)
            if isinstance(response, Exception):
                raise response
            return response

        notifier = watchdog.FeishuNotifier(
            make_config(), self.logger, retry_delays=(1,), sleep=lambda delay: None, opener=opener
        )
        self.assertTrue(notifier.send("hello"))


class MessageTests(unittest.TestCase):
    def test_formats_normal_and_abnormal_conclusions(self):
        normal = watchdog.ServiceStatus(True, "running", 123, True, 4, "退出码 0", "ok")
        abnormal = watchdog.ServiceStatus(False, "未加载", None, False, None, "无记录", "missing")
        now = watchdog.datetime.now().astimezone()
        self.assertIn("结论: 正常", watchdog.format_message(make_config(), normal, now))
        self.assertIn("结论: 异常", watchdog.format_message(make_config(), abnormal, now))


if __name__ == "__main__":
    unittest.main()
