"""Open review-only pull requests for stable 3X-UI upstream releases.

Never write to main, merge PRs automatically, or deploy to VPN nodes.
"""
import json
import os
from pathlib import Path
import re
import subprocess


UPSTREAM = "MHSanaei/3x-ui"
BRANCH_PREFIX = "updates/3x-ui-"


def command(*args, check=True):
    return subprocess.run(args, check=check, text=True, capture_output=True)


def gh_json(*args):
    return json.loads(command("gh", *args).stdout)


def prepare(repo, upstream=UPSTREAM, base="main"):
    release = gh_json("api", f"repos/{upstream}/releases/latest")
    tag = release["tag_name"]
    if (release.get("draft") or release.get("prerelease")
            or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,100}", tag)):
        raise ValueError("Expected a stable 3X-UI release with a safe tag")

    pending = gh_json("pr", "list", "--repo", repo, "--state", "open",
                      "--limit", "100", "--json", "headRefName")
    if any(pr["headRefName"].startswith(BRANCH_PREFIX) for pr in pending):
        print("An upstream PR is already open; review it before another update.")
        return

    # Shallow fetches cannot reliably determine merge ancestry.
    command("git", "fetch", "--no-tags",
            f"https://github.com/{upstream}.git", f"refs/tags/{tag}")
    sha = command("git", "rev-parse", "FETCH_HEAD^{commit}").stdout.strip()
    present = command("git", "merge-base", "--is-ancestor", sha, "HEAD", check=False)
    if present.returncode == 0:
        print(f"Upstream release {tag} is already included.")
        return
    if present.returncode != 1:
        raise RuntimeError(present.stderr or "Could not check upstream ancestry")

    branch = BRANCH_PREFIX + sha[:12]
    existing = command("git", "ls-remote", "--heads", "origin", f"refs/heads/{branch}")
    if existing.stdout.strip():
        print("Update branch already exists; inspect its PR before retrying.")
        return

    command("git", "config", "user.name", "ElX Upstream Sync[bot]")
    command("git", "config", "user.email", "41898282+github-actions[bot]@users.noreply.github.com")
    command("git", "checkout", "-b", branch)
    merged = command("git", "merge", "--no-ff", "-m", f"Merge 3X-UI {tag}", sha, check=False)
    conflict = merged.returncode != 0
    if conflict:
        paths = command("git", "diff", "--name-only", "--diff-filter=U").stdout.splitlines()
        if not paths:
            raise RuntimeError(merged.stderr or merged.stdout)
        command("git", "merge", "--abort")
        record = Path(".github/upstream-conflict.json")
        record.parent.mkdir(parents=True, exist_ok=True)
        record.write_text(json.dumps({
            "upstream": upstream, "tag": tag, "sha": sha, "conflicts": paths
        }, indent=2) + "\n")
        command("git", "add", str(record))
        command("git", "commit", "-m", f"Record conflicts merging 3X-UI {tag}")
        description = ("The upstream merge CONFLICTED. This branch contains only a "
                       "conflict report, NOT the upstream changes. Resolve locally, "
                       "remove .github/upstream-conflict.json and rerun CI.\n\n"
                       + "\n".join(f"- `{p}`" for p in paths))
    else:
        description = (f"Merge stable 3X-UI release {tag} ({sha}) into ElX-ui. "
                       "Review local Control integration, migrations, public API, "
                       "subscription behavior and customer portal before merging. "
                       "No automatic merge or deployment.")

    command("git", "push", "origin", f"HEAD:refs/heads/{branch}")
    args = ["pr", "create", "--repo", repo, "--base", base, "--head", branch,
            "--title", f"Update 3X-UI to {tag}", "--body", description, "--draft"]
    print(command("gh", *args).stdout.strip())


if __name__ == "__main__":
    if not os.environ.get("GH_TOKEN"):
        raise SystemExit("GitHub App token is required")
    prepare(os.environ["GITHUB_REPOSITORY"])
