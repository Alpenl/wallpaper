"""提交总结检查的纯函数和真实 Git 暂存区/钩子回归。"""
import importlib.util
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("commit_summary", ROOT / "scripts/check-commit-summary.py")
CHECK = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK)


def entry(topic="本次改动"):
    return (f"## 2026-11-05 | {topic}\n\n"
            "- 目的：修复回归。\n- 改动：更新代码和测试。\n"
            "- 验证：运行定点测试通过。\n- 限制：未验证真机。\n\n")


BASE = "# 提交总结\n\n" + entry("历史记录")


class ValidationTests(unittest.TestCase):
    def validate(self, current, previous=BASE, inherited=None, required=True):
        CHECK.validate(previous, current, inherited or set(), required)

    def test_append(self):
        self.validate(BASE + entry())

    def test_missing_or_whitespace(self):
        for text in (BASE, BASE + "\n", BASE + "随意文字\n"):
            with self.subTest(text=text), self.assertRaises(CHECK.SummaryError):
                self.validate(text)

    def test_rewriting_history(self):
        with self.assertRaises(CHECK.SummaryError):
            self.validate(BASE.replace("历史记录", "已修改历史") + entry())

    def test_all_fields_required(self):
        for field in CHECK.FIELDS:
            with self.subTest(field=field), self.assertRaises(CHECK.SummaryError):
                self.validate(BASE + entry().replace(f"- {field}：", "- 其他："))

    def test_empty_field_cannot_consume_next_line(self):
        with self.assertRaises(CHECK.SummaryError):
            self.validate(BASE + entry().replace("- 验证：运行定点测试通过。", "- 验证：  "))

    def test_duplicate_heading(self):
        with self.assertRaises(CHECK.SummaryError):
            self.validate(BASE + entry("历史记录"))

    def test_invalid_date(self):
        with self.assertRaises(ValueError):
            self.validate(BASE + entry().replace("2026-11-05", "2026-02-30"))

    def test_merge_inherited_entry_is_not_a_new_summary(self):
        inherited = {"## 2026-11-05 | 来自另一分支"}
        with self.assertRaises(CHECK.SummaryError):
            self.validate(BASE + entry("来自另一分支"), inherited=inherited)
        self.validate(BASE + entry("来自另一分支") + entry("合并总结"), inherited=inherited)


class GitHookTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="wallpaper summary 中文 ")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.env = {k: v for k, v in os.environ.items() if not k.startswith("GIT_")}
        self.env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull)
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.name", "Summary test")
        self.git("config", "user.email", "summary@example.invalid")
        self.git("config", "core.autocrlf", "false")
        self.write("README.md", "# Fixture\n")
        self.write("example.go", "package example\n")
        self.write(CHECK.SUMMARY, BASE)
        self.git("add", ".")
        self.git("commit", "-qm", "fixture baseline")
        for name in ("scripts/check-commit-summary.py", ".githooks/pre-commit", ".githooks/pre-merge-commit"):
            target = self.root / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / name, target)
            target.chmod(0o755)
        self.git("config", "core.hooksPath", ".githooks")

    def write(self, name, text):
        target = self.root / name
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text, encoding="utf-8")

    def git(self, *args, expected=0):
        result = subprocess.run(["git", *args], cwd=self.root, env=self.env,
                                capture_output=True, encoding="utf-8", errors="replace")
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        return result

    def change_code(self):
        self.write("example.go", "package example\n// changed\n")
        self.git("add", "example.go")

    def test_blocks_code_without_summary(self):
        self.change_code()
        result = self.git("commit", "-qm", "missing", expected=1)
        self.assertIn("new staged entry", result.stderr)

    def test_unstaged_summary_does_not_count(self):
        self.change_code()
        self.write(CHECK.SUMMARY, BASE + entry())
        self.git("commit", "-qm", "unstaged", expected=1)

    def test_staged_summary_allows_and_next_commit_needs_another(self):
        self.change_code()
        self.write(CHECK.SUMMARY, BASE + entry())
        self.git("add", CHECK.SUMMARY)
        self.git("commit", "-qm", "with summary")
        self.write("example.go", "package example\n// next change\n")
        self.git("commit", "-qam", "next without summary", expected=1)

    def test_worktree_history_rewrite_does_not_affect_staged_summary(self):
        self.change_code()
        self.write(CHECK.SUMMARY, BASE + entry())
        self.git("add", CHECK.SUMMARY)
        self.write(CHECK.SUMMARY, "not staged\n")
        self.git("commit", "-qm", "uses staged version")
        self.assertEqual(self.git("show", "HEAD:" + CHECK.SUMMARY).stdout, BASE + entry())

    def test_commit_all_and_path_selection(self):
        self.change_code()
        self.write(CHECK.SUMMARY, BASE + entry())
        # 按路径提交代码会忽略工作区的总结；-a 则把两个已跟踪文件都加入提交。
        self.git("commit", "-qm", "code only", "--", "example.go", expected=1)
        self.git("commit", "-qam", "tracked code and summary")

    def test_markdown_only_is_allowed(self):
        self.write("README.md", "# Updated fixture\n")
        self.git("commit", "-qam", "documentation only")

    def test_resource_and_code_deletion_require_summary(self):
        self.git("rm", "example.go")
        self.git("commit", "-qm", "delete code", expected=1)
        self.git("reset", "--hard", "HEAD")
        self.write("packaging/fixture.json", "{}\n")
        self.git("add", "packaging/fixture.json")
        self.git("commit", "-qm", "config", expected=1)

    def test_summary_deletion_is_rejected(self):
        self.git("rm", CHECK.SUMMARY)
        self.git("commit", "-qm", "delete history", expected=1)

    def test_amend_with_code_requires_an_additional_entry(self):
        self.change_code()
        self.git("commit", "--amend", "--no-edit", expected=1)
        self.write(CHECK.SUMMARY, BASE + entry("修订"))
        self.git("add", CHECK.SUMMARY)
        self.git("commit", "--amend", "--no-edit")

    def test_merge_requires_summary_not_just_feature_entry(self):
        self.git("checkout", "-qb", "feature")
        self.change_code()
        self.write(CHECK.SUMMARY, BASE + entry("分支改动"))
        self.git("add", CHECK.SUMMARY)
        self.git("commit", "-qm", "feature")
        self.git("checkout", "-q", "main")
        self.git("merge", "--no-ff", "--no-edit", "feature", expected=1)
        self.git("commit", "-qm", "inherited summary only", expected=1)
        self.write(CHECK.SUMMARY, BASE + entry("分支改动") + entry("合并总结"))
        self.git("add", CHECK.SUMMARY)
        self.git("commit", "-qm", "merge with summary")

    def test_initial_commit(self):
        self.git("checkout", "--orphan", "initial")
        self.write(CHECK.SUMMARY, "# 提交总结\n\n" + entry("初始化"))
        self.git("add", CHECK.SUMMARY)
        self.git("commit", "-qm", "initial with summary")

    def test_session_start_reports_hook_configuration(self):
        result = subprocess.run([sys.executable, str(ROOT / "scripts/check-commit-summary.py"),
                                 "--claude-context"], cwd=self.root, env=self.env,
                                capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        import json
        payload = json.loads(result.stdout)["hookSpecificOutput"]
        self.assertEqual(payload["hookEventName"], "SessionStart")
        self.assertIn("已配置", payload["additionalContext"])


if __name__ == "__main__":
    unittest.main()
