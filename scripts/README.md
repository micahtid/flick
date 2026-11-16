# Scripts

Stops storage nodes at random and checks that no chunk is lost. Run the scripts from the project folder while `docker compose up` is running. They need `curl` and `shuf`, which come with Git Bash on Windows.

## Commands

| Command | Description |
| --- | --- |
| `sh scripts/chaos.sh` | Stops and starts a random storage node repeatedly for 10 minutes. |
| `sh scripts/check.sh` | Checks that every chunk in the `files` table is saved on all 3 nodes. It fails if any chunk is missing a copy. |

## Development

| Command | Description |
| --- | --- |
| `sh -n scripts/<name>.sh` | Checks a script for mistakes without running it. |

## Additional Information: The Chaos Test

| Step | Description |
| --- | --- |
| 1 | Run `sh scripts/chaos.sh`. |
| 2 | While it runs, keep uploading videos from a second terminal, such as with `while true; do curl -F file=@test.mp4 -F title=Test localhost/api/videos; sleep 8; done`. |
| 3 | Watch the **Storage Nodes Up** and **Raft Leader** panels in Grafana, at http://localhost:3001. Each stopped node turns red, and if it was the leader, another node takes over within a few seconds. |
| 4 | When it finishes, wait a few seconds for the repair loop, then run `sh scripts/check.sh`. Every chunk should have 3 copies, which proves none was lost while nodes kept failing. |
