"""Exercise Markdown length boundaries and links in a disposable repository."""
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


class PublicDocumentationPolicy(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        subprocess.run(['git', 'init', '-q', str(self.root)], check=True)
        self.checker = self.root / 'scripts/ci/check-public-docs.py'
        self.checker.parent.mkdir(parents=True)
        shutil.copyfile(Path(__file__).with_name('check-public-docs.py'), self.checker)

    def check(self):
        return subprocess.run([sys.executable, str(self.checker)],
                              capture_output=True, text=True)

    def test_exactly_500_words_pass(self):
        (self.root / 'guide.md').write_text('word ' * 500)
        result = self.check()
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_501_words_fail_with_correct_limit(self):
        (self.root / 'guide.md').write_text('word ' * 501)
        result = self.check()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('guide.md: 501 words; keep each guide within 500 words.', result.stderr)

    def test_governance_exception_still_checks_links(self):
        policy = self.root / 'CODE_OF_CONDUCT.md'
        policy.write_text('word ' * 501)
        self.assertEqual(self.check().returncode, 0)
        policy.write_text('word ' * 501 + '[missing](missing.md)')
        result = self.check()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('CODE_OF_CONDUCT.md: missing local link missing.md', result.stderr)
        self.assertNotIn('within 500 words', result.stderr)


if __name__ == '__main__':
    unittest.main()
