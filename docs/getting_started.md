As of the current version, this document and its contents are written and maintained by a human.

### 1. Start the server

```sh
reviewer serve [--port PORT] [--data-dir DIR]
```

Listens on `http://127.0.0.1:8091`. Data is stored in `~/.config/reviewer/`.

You can also use the systemd unit available in docs/examples/reviewer.service

### 2. Register the project

Enter the project directory. If it is not already a git repository, create one and add an initial commit. This tooling currently assumes the primary branch is 'main'.

Register the project: `reviewer init`

This adds a `review` remote to the repo you're in, and will automatically push `main` to that remote.  Note that the `review` remote is a bare repository stored locally in the data directory (default: `~/.config/reviewer/repos/<project-name>.git`).

Complete example, including adding an initial commit on a new repository:

```sh
cd ~/projects/my-project
git init
touch README.md
git add README.md
git commit -m 'Initial commit'
reviewer init  [--port PORT]
```
*note*: If your server is not on the default port, pass `--port` so the hook URL is correct.

### 3. Open a review

Any push of a non-`main` branch to `review` automatically creates a review:

```sh
git push review my-branch
```

The review URL will be output by the post-receive hook.

### 5. Review the change

The web UI shows the diff, lets you leave inline comments (including multi-line). When done, choose the Review option frm the floating controls.  You can choose one of the three actions, and include a review-level comment:

- **Approve** — marks the review accepted
- **Request Changes** — leaves it open; the agent revises and pushes again to the same branch
- **Pause** — halts agent - based in instructions you give this will typically require the agent to take no action without further interactive input.
For anything other than 'approve', the agent (when instructed) will see the resulting feedback from the `wair` command, and implement any requested changes. 

To abandon the work entirely, delete the branch from the review remote
(`git push review :<branch>`); that closes any open review. 

### 6. Agent workflow

Agents block on the review outcome by running the following from within the repo:

```sh
reviewer wait
```

This returns once review status changes. The plain-text output
includes status (approved, changes_requested, paused), and comments with file/line anchors.
