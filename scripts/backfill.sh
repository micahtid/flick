#!/bin/sh
# Adds chunks saved before Phase 5 to the chunk map. Until then, the repair
# loop can't see them, so a copy that's missing is never filled in. Run it
# once from the project folder, with every node running. Running it again
# does no harm.

# Ask node1 which node is the leader, such as node2, and keep its number. The
# leader's port is 9000 plus that number. Changes go straight to the leader,
# since a follower would redirect to an address only Docker can reach.
leader=$(curl -s localhost:9001/status | sed 's/.*"leader":"node\([0-9]\)".*/\1/')
case "$leader" in
  [1-3]) ;;
  *) echo "no leader yet, try again in a few seconds"; exit 1 ;;
esac

added=0
for id in $(docker compose exec -T db psql -U postgres -At -c "SELECT DISTINCT unnest(chunks) FROM files"); do
  for n in 1 2 3; do
    if curl -sf -o /dev/null "http://localhost:900$n/chunks/$id"; then
      curl -sf -X POST "http://localhost:900$leader/map" \
        -d "{\"op\":\"add\",\"chunk_id\":\"$id\",\"nodes\":[\"http://node$n:900$n\"]}" || exit 1
      added=$((added + 1))
    fi
  done
done

echo "$added copies added to the map. The repair loop fills in the rest within a few seconds."
