#!/bin/sh
# Checks that every chunk in the files table is saved on all 3 storage nodes.
# Run it from the project folder, with every node running. The repair loop
# needs a few seconds to fill in a node that has just come back.

total=0
short=0
for id in $(docker compose exec -T db psql -U postgres -At -c "SELECT DISTINCT unnest(chunks) FROM files"); do
  copies=0
  for port in 9001 9002 9003; do
    # A node replies 200 only if its copy matches the chunk's fingerprint.
    if curl -sf -o /dev/null "http://localhost:$port/chunks/$id"; then
      copies=$((copies + 1))
    fi
  done
  total=$((total + 1))
  if [ "$copies" -lt 3 ]; then
    echo "chunk $id has $copies of 3 copies"
    short=$((short + 1))
  fi
done

echo "$total chunks checked, $short missing a copy"
[ "$short" -eq 0 ]
