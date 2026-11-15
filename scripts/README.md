# Scripts

Shell scripts for testing the storage nodes and upgrading old data. Run them from the project folder while `docker compose up` is running. They need `curl` and `shuf`, which come with Git Bash on Windows.

## Commands

| Command | Description |
| --- | --- |
| `sh scripts/chaos.sh` | Stops and starts a random storage node, over and over, for 10 minutes. |
| `sh scripts/check.sh` | Checks that every chunk in the `files` table is saved on all 3 nodes. It fails if any chunk is missing a copy. |
| `sh scripts/backfill.sh` | Adds chunks saved before Phase 5 to the chunk map, so the repair loop can see them. Run it once after upgrading. |

## Development

| Command | Description |
| --- | --- |
| `sh -n scripts/<name>.sh` | Checks a script for mistakes without running it. |

## Additional Information: The Chaos Test

The chaos test proves that no chunk is lost while nodes keep failing.

| Step | Description |
| --- | --- |
| 1 | Run `sh scripts/chaos.sh`. |
| 2 | While it runs, keep uploading videos in a second terminal, such as with `while true; do curl -F file=@test.mp4 -F title=Test localhost/api/videos; sleep 8; done`. |
| 3 | When it finishes, wait a few seconds for the repair loop, then run `sh scripts/check.sh`. Every chunk should have 3 copies. |
