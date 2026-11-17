"""Exercise repository throttling recovery without downloads or real delays."""
import importlib.util
import io
from pathlib import Path
import unittest
from unittest.mock import MagicMock, patch

spec = importlib.util.spec_from_file_location('retry_android', Path(__file__).with_name('retry-android.py'))
retry = importlib.util.module_from_spec(spec)
spec.loader.exec_module(retry)


class AndroidRetry(unittest.TestCase):
    def invoke(self, results):
        processes = []
        for code, output in results:
            process = MagicMock()
            process.__enter__.return_value = process
            process.stdout = io.StringIO(output)
            process.wait.return_value = code
            processes.append(process)
        with patch.object(retry.subprocess, 'Popen', side_effect=processes) as popen, \
                patch.object(retry.time, 'sleep') as sleep, \
                patch('sys.stdout', new_callable=io.StringIO):
            code = retry.run(['gradlew', 'test'])
        return code, popen.call_count, [call.args[0] for call in sleep.call_args_list]

    def test_throttle_then_success(self):
        self.assertEqual(self.invoke([(1, 'Received status code 429 from server: Too Many Requests'),
                                      (0, 'BUILD SUCCESSFUL')]), (0, 2, [20]))

    def test_retries_are_bounded(self):
        self.assertEqual(self.invoke([(1, 'Received status code 429 from server: throttle')] * 3),
                         (1, 3, [20, 40]))

    def test_compile_failure_is_immediate(self):
        self.assertEqual(self.invoke([(1, 'Compilation error')]), (1, 1, []))

    def test_success_is_immediate(self):
        self.assertEqual(self.invoke([(0, 'BUILD SUCCESSFUL')]), (0, 1, []))
