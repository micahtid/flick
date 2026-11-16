#!/bin/sh
# This script stops and starts a random storage node, over and over, for 10
# minutes. Keep uploading videos while it runs, then run scripts/check.sh to
# make sure no chunk was lost. Run it from the project folder.

end=$(( $(date +%s) + 600 )) # 600 seconds is 10 minutes.
while [ "$(date +%s)" -lt "$end" ]; do
  n=node$(shuf -i 1-3 -n 1) # Pick node1, node2 or node3.
  echo "stopping $n"
  docker compose stop "$n"
  sleep "$(shuf -i 5-20 -n 1)"
  echo "starting $n"
  docker compose start "$n"
  sleep "$(shuf -i 5-20 -n 1)"
done
