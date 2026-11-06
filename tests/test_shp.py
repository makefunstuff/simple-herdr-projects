"""Tests for shp's core: the store, spaces, worktrees, workers and the queue.

Every test runs in a throwaway HOME with a real git repo and fake hc/herdr-app.
"""

import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SHP = ROOT / "shp"
FAKES = Path(__file__).resolve().parent / "fakes"


class ShpTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name)
        self.home = root / "home"
        self.home.mkdir()
        self.state = root / "state"
        self.state.mkdir()
        self.bin = root / "bin"
        self.bin.mkdir()
        for name in ("hc", "herdr-app"):
            shutil.copy(FAKES / name, self.bin / name)
            (self.bin / name).chmod(0o755)
        self.store = self.home / ".herdr" / "simple"
        self.repo = self.make_repo("app")
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        self.env = {**env, "HOME": str(self.home), "PATH": f"{self.bin}:{env['PATH']}",
                    "FAKE_STATE": str(self.state)}

    def make_repo(self, name):
        repo = Path(self.temp.name) / name
        repo.mkdir()
        self.git("init", "-q", cwd=repo)
        self.git("symbolic-ref", "HEAD", "refs/heads/main", cwd=repo)
        (repo / "README.md").write_text("start\n")
        self.git("add", ".", cwd=repo)
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.com",
                 "commit", "-qm", "start", cwd=repo)
        return repo

    def git(self, *args, cwd):
        return subprocess.run(["git", *args], cwd=cwd, check=True,
                              capture_output=True, text=True).stdout

    def shp(self, *args, stdin=None):
        return subprocess.run([sys.executable, str(SHP), *args], env=self.env,
                              capture_output=True, text=True, input=stdin)

    def ok(self, *args, stdin=None):
        done = self.shp(*args, stdin=stdin)
        self.assertEqual(done.returncode, 0, f"shp {args}: {done.stderr}")
        return done.stdout

    def refused(self, *args):
        done = self.shp(*args)
        self.assertEqual(done.returncode, 1, f"shp {args} did not refuse: {done.stdout}")
        return done.stderr

    def make_space(self, name="work", repo=None, *extra):
        self.ok("space", "add", name, "--folder", str(repo or self.repo), *extra)

    def add_task(self, title, *extra, space="work", stdin=None):
        return self.ok("add", title, "--space", space, *extra, stdin=stdin).strip()

    def task_path(self, task_id, space="work"):
        return self.store / "spaces" / space / f"{task_id}.md"

    def header(self, task_id, space="work"):
        head = {}
        lines = self.task_path(task_id, space).read_text().splitlines()
        for line in lines[1:lines.index("---", 1)]:
            key, _, value = line.partition(":")
            try:
                head[key.strip()] = json.loads(value.strip())
            except ValueError:
                head[key.strip()] = value.strip()
        return head

    def set_status(self, task_id, status, space="work"):
        path = self.task_path(task_id, space)
        text = path.read_text()
        old = f"\nstatus: {self.header(task_id, space)['status']}\n"
        self.assertIn(old, text)
        path.write_text(text.replace(old, f"\nstatus: {status}\n", 1))

    def write_task(self, task_id, text, space="work"):
        path = self.task_path(task_id, space)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def calls(self, name):
        path = self.state / name
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def fact(self, name, value):
        (self.state / name).write_text(json.dumps(value))

    def start_task(self, task_id):
        return self.ok("start", task_id).strip()

    def worker(self, task_id, address="t9"):
        self.fact("ls.json", {"terminals": [{
            "address": address, "state": "live", "created_ms": 1,
            "labels": {"herdr.role": "worker", "herdr.task": task_id}}]})


class SpaceTest(ShpTest):
    def test_space_file_holds_the_repo_and_a_derived_prefix(self):
        self.make_space("work", self.repo, "--checks", '[["make", "check"]]')
        path = self.store / "spaces" / "work.md"
        text = path.read_text()
        self.assertIn('prefix: WORK', text)
        self.assertIn("base: main", text)
        self.assertIn('"make"', text)
        self.assertIn(str(self.repo), text)
        self.assertIn("next: 1", text)

    def test_a_taken_prefix_gets_a_digit(self):
        other = self.make_repo("api")
        self.make_space("my-app", self.repo)
        self.make_space("my-api", other)
        self.assertIn("prefix: MA\n", (self.store / "spaces" / "my-app.md").read_text())
        self.assertIn("prefix: MA2\n", (self.store / "spaces" / "my-api.md").read_text())

    def test_a_space_must_be_a_git_repo_root_with_its_base_branch(self):
        plain = Path(self.temp.name) / "plain"
        plain.mkdir()
        self.assertIn("not a git repo", self.refused("space", "add", "x", "--folder", str(plain)))
        self.assertIn("not a git repo", self.refused("space", "add", "x", "--folder", str(self.repo / "README.md")))
        self.assertIn("git", self.refused("space", "add", "x", "--folder", str(self.repo), "--base", "nope"))

    def test_config_prints_the_defaults_before_any_store_exists(self):
        self.assertIn("max_running: 4", self.ok("config"))


class AddTest(ShpTest):
    def test_ids_are_never_reused(self):
        self.make_space()
        self.assertEqual(self.add_task("First"), "WORK-1")
        self.assertEqual(self.add_task("Second"), "WORK-2")
        self.task_path("WORK-1").unlink()
        self.assertEqual(self.add_task("Third"), "WORK-3")

    def test_a_task_is_a_file_with_its_branch_worktree_and_brief(self):
        self.make_space()
        task_id = self.add_task("Add a search box", "--brief", "-", stdin="Put a box on top.\n")
        head = self.header(task_id)
        self.assertEqual(head["status"], "todo")
        self.assertEqual(head["branch"], "task/1-add-a-search-box")
        self.assertEqual(head["worktree"], "~/.herdr/worktrees/app/task-1-add-a-search-box")
        self.assertEqual(head["asks"], [])
        self.assertIn("Put a box on top.", self.ok("show", task_id))
        shown = json.loads(self.ok("list", "--json"))
        self.assertEqual([task["id"] for task in shown], [task_id])

    def test_a_task_may_wait_for_a_parent_and_after(self):
        self.make_space()
        parent = self.add_task("Parent")
        sibling = self.add_task("Sibling")
        child = self.add_task("Child", "--status", "queued", "--parent", parent,
                               "--after", sibling)
        head = self.header(child)
        self.assertEqual(head["parent"], parent)
        self.assertEqual(head["after"], [sibling])
        self.assertIn("no task WORK-9", self.refused("add", "X", "--space", "work", "--parent", "WORK-9"))


class ListTest(ShpTest):
    def test_todo_queued_working_and_needs_show(self):
        self.make_space()
        todo = self.add_task("A todo task")
        queued = self.add_task("A queued task", "--status", "queued")
        working = self.add_task("A working task")
        waiting = self.add_task("A asking task")
        self.start_task(working)
        self.start_task(waiting)
        self.ok("ask", waiting, "Which colour?")
        text = self.ok("list")
        lines = {line.split()[0]: line for line in text.splitlines()}
        self.assertIn(" todo ", lines[todo])
        self.assertIn(" queued ", lines[queued])
        self.assertIn(" working ", lines[working])
        self.assertIn(" needs ", lines[waiting])
        self.assertIn("Which colour?", lines[waiting])

    def test_a_bad_file_names_its_path_and_the_rest_still_list(self):
        self.make_space()
        good = self.add_task("Good")
        self.write_task("WORK-9", "no header here\n")
        done = self.shp("list")
        self.assertEqual(done.returncode, 1)
        self.assertIn("WORK-9.md", done.stderr)
        self.assertIn(good, done.stdout)

    def test_unknown_keys_and_unknown_statuses_survive_reads(self):
        self.make_space()
        self.write_task("WORK-1", """---
id: WORK-1
title: Odd
status: parked
custom: 7
branch: task/1-odd
worktree: ~/.herdr/worktrees/app/task-1-odd
---
Brief.
""")
        self.assertIn("parked", self.ok("list"))
        self.assertIn("parked", self.refused("start", "WORK-1"))
        self.ok("status", "WORK-1", "--progress", "10")
        text = self.task_path("WORK-1").read_text()
        self.assertIn("status: parked", text)
        self.assertIn("custom: 7", text)

    def test_a_file_missing_status_is_todo_and_runs(self):
        self.make_space()
        self.write_task("WORK-7", """---
id: WORK-7
title: No status
branch: task/7-no-status
worktree: ~/.herdr/worktrees/app/task-7-no-status
---
Do the thing.
""")
        self.assertIn("WORK-7  todo  No status", self.ok("list"))
        self.start_task("WORK-7")
        self.assertEqual(self.header("WORK-7")["status"], "working")
        self.assertTrue((self.home / ".herdr/worktrees/app/task-7-no-status").is_dir())


class WorkerTest(ShpTest):
    def test_a_worker_is_launched_labelled_and_reports(self):
        self.make_space()
        task_id = self.add_task("Add a search box", "--agent", '["claude"]')
        self.start_task(task_id)
        launch = self.calls("app.log")[0]
        self.assertEqual(launch[:2], ["do", "space.new"])
        args = json.loads(launch[2])
        self.assertEqual(args["name"], f"{task_id} · Add a search box")
        self.assertEqual(args["tab"], "Chat")
        self.assertEqual(args["argv"], ["claude"])
        self.assertEqual(args["folder"], str(self.home / ".herdr/worktrees/app/task-1-add-a-search-box"))
        task_file = str(self.task_path(task_id)).replace(str(self.home), "~", 1)
        self.assertIn(task_file, args["task"])
        self.assertIn(["label", "t9", f"herdr.task={task_id}", "herdr.role=worker"], self.calls("hc.log"))
        head = self.header(task_id)
        self.assertEqual(head["status"], "working")
        self.assertEqual(head["workspace"], "sp-test")
        worktree = self.home / ".herdr/worktrees/app/task-1-add-a-search-box"
        self.assertEqual(self.git("rev-parse", "--abbrev-ref", "HEAD", cwd=worktree).strip(),
                         "task/1-add-a-search-box")
        self.ok("status", task_id, "--progress", "40", "--step", "Writing the box", "--left", "1h")
        line = self.ok("list")
        self.assertIn("40% Writing the box", line)
        self.assertIn("1h left", line)

    def test_a_first_message_that_did_not_go_in_is_read_then_retried(self):
        self.make_space()
        task_id = self.add_task("Add a search box")
        self.fact("get.json", {"state": "live", "first_message": {"sent": False, "reason": "timeout"}})
        self.start_task(task_id)
        calls = self.calls("hc.log")
        sends = [call for call in calls if call[0] == "send"]
        self.assertEqual(len(sends), 1)
        self.assertEqual(sends[0][1], "t9")
        task_file = str(self.task_path(task_id)).replace(str(self.home), "~", 1)
        self.assertIn(task_file, sends[0][2])
        first_send = calls.index(sends[0])
        self.assertTrue(any(call[0] == "read" for call in calls[:first_send]))
        self.assertTrue(any(call[0] == "get" for call in calls[:first_send]))
        self.assertEqual(self.header(task_id)["status"], "working")

    def test_a_worker_that_never_takes_its_task_leaves_the_task_todo(self):
        self.make_space()
        task_id = self.add_task("Add a search box", "--status", "queued")
        self.fact("get.json", {"state": "live", "first_message": {"sent": False, "reason": "timeout"}})
        (self.state / "send_fails").write_text("")
        self.assertIn("first message was not sent", self.refused("start", task_id))
        self.assertIn(["do", "space.close", json.dumps({"id": "sp-test", "confirmed": True})],
                      self.calls("app.log"))
        self.assertEqual(self.header(task_id)["status"], "queued")

    def test_start_refuses_a_task_that_already_has_a_live_worker(self):
        self.make_space()
        task_id = self.add_task("Already running")
        self.worker(task_id)
        self.assertIn("already has a live worker", self.refused("start", task_id))

    def test_only_todo_and_queued_start(self):
        self.make_space()
        task_id = self.add_task("Once")
        self.start_task(task_id)
        self.assertIn("only todo or queued starts", self.refused("start", task_id))


class AskTest(ShpTest):
    def test_an_ask_moves_to_needs_and_an_answer_closes_it(self):
        self.make_space()
        task_id = self.add_task("Add a search box")
        self.start_task(task_id)
        self.worker(task_id)
        self.ok("ask", task_id, "Which colour?")
        head = self.header(task_id)
        self.assertEqual(head["status"], "needs")
        self.assertEqual(len(head["asks"]), 1)
        self.assertIn("needs", self.ok("list"))
        self.ok("say", task_id, "Use blue")
        head = self.header(task_id)
        self.assertEqual(head["status"], "working")
        self.assertEqual(head["asks"], [])
        self.assertIn(["send", "t9", "Use blue"], self.calls("hc.log"))

    def test_a_send_that_fails_keeps_the_ask_open(self):
        self.make_space()
        task_id = self.add_task("Add a search box")
        self.start_task(task_id)
        self.worker(task_id)
        self.ok("ask", task_id, "Which colour?")
        (self.state / "send_fails").write_text("")
        self.assertIn("still needs you", self.refused("say", task_id, "Use blue"))
        head = self.header(task_id)
        self.assertEqual(head["status"], "needs")
        self.assertEqual(len(head["asks"]), 1)

    def test_say_without_a_live_worker_keeps_the_ask(self):
        self.make_space()
        task_id = self.add_task("Add a search box")
        self.start_task(task_id)
        self.ok("ask", task_id, "Which colour?")
        self.assertIn("no live worker", self.refused("say", task_id, "Use blue"))
        self.assertEqual(self.header(task_id)["status"], "needs")

    def test_several_asks_need_asking_for_one(self):
        self.make_space()
        task_id = self.add_task("Add a search box")
        self.start_task(task_id)
        self.worker(task_id)
        self.ok("ask", task_id, "First?")
        self.ok("ask", task_id, "Second?")
        asks = self.header(task_id)["asks"]
        self.assertIn("--ask", self.refused("say", task_id, "Both"))
        self.ok("say", task_id, "One", "--ask", asks[0]["id"])
        head = self.header(task_id)
        self.assertEqual(head["status"], "needs")
        self.assertEqual([ask["id"] for ask in head["asks"]], [asks[1]["id"]])
        self.ok("say", task_id, "Two", "--ask", asks[1]["id"])
        self.assertEqual(self.header(task_id)["status"], "working")


class QueueTest(ShpTest):
    def write_config(self, text):
        (self.store / "config.md").write_text(text)

    def test_slots_cap_running_workers_and_tick_only_starts_queued(self):
        self.make_space()
        self.write_config("---\nworker: [\"pi\"]\nmax_running: 1\n---\n")
        first = self.add_task("First", "--status", "queued")
        second = self.add_task("Second", "--status", "queued")
        todo = self.add_task("Never by tick")
        self.assertEqual(self.ok("tick").strip(), first)
        self.assertEqual(self.header(first)["status"], "working")
        self.assertEqual(self.header(second)["status"], "queued")
        self.assertEqual(self.ok("tick").strip(), "")
        self.set_status(first, "done")
        self.assertEqual(self.ok("tick").strip(), second)
        self.assertEqual(self.header(todo)["status"], "todo")

    def test_a_child_waits_for_its_parent_and_after(self):
        self.make_space()
        parent = self.add_task("Parent")
        sibling = self.add_task("Sibling")
        child = self.add_task("Child", "--status", "queued", "--parent", parent, "--after", sibling)
        self.assertEqual(self.ok("tick").strip(), "")
        self.set_status(parent, "done")
        self.assertEqual(self.ok("tick").strip(), "")
        self.set_status(sibling, "done")
        self.assertEqual(self.ok("tick").strip(), child)
        self.assertEqual(self.header(child)["status"], "working")

    def test_a_needs_worker_still_holds_its_slot(self):
        self.make_space()
        self.write_config("---\nworker: [\"pi\"]\nmax_running: 1\n---\n")
        wait = self.add_task("Waiting")
        other = self.add_task("Other", "--status", "queued")
        self.start_task(wait)
        self.ok("ask", wait, "Well?")
        self.assertEqual(self.ok("tick").strip(), "")
        self.assertEqual(self.header(other)["status"], "queued")


if __name__ == "__main__":
    unittest.main()
