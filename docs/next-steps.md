# Next Steps: Real Machines

All three storage nodes run on one PC, so messages between them never really leave the computer. Real networks are messier: messages can be slow, arrive late or vanish, and a whole machine can lose power. This step proves that Flick still works when the nodes are on separate machines.

## Step 1: Practice on a Bad Network at Home

Before using real machines, fake a bad network on your own PC. `tc` is a Linux tool that can slow down a container's network on purpose. The `nicolaka/netshoot` image has it, and `--net container:` runs it inside node2's network:

```sh
# Add a 300 ms delay that varies by up to 100 ms either way
docker run --rm --net container:flick-node2-1 --cap-add NET_ADMIN \
  nicolaka/netshoot tc qdisc add dev eth0 root netem delay 300ms 100ms

# Remove the delay
docker run --rm --net container:flick-node2-1 --cap-add NET_ADMIN \
  nicolaka/netshoot tc qdisc del dev eth0 root
```

To cut node2 off completely, freeze it. It stops answering, the same as a node the network can't reach, and keeps its address:

```sh
docker compose pause node2
docker compose unpause node2
```

Watch the Flick dashboard in Grafana while you do this.

**What should happen:** when node2 is cut off, the other two elect a leader if needed and keep working. When node2 is reconnected, it catches up.

**If leaders keep changing again and again,** the delay is making nodes think the leader is dead. Give them more patience by raising `HeartbeatTimeout` and `ElectionTimeout` in `startRaft` in `storage/cluster.go`, such as to 2 seconds each.

## Step 2: Get Three Machines

Any of these work:

- Three small cloud servers, about 1 CPU and 1 GB of memory each.
- Spare laptops or Raspberry Pis on your home network.

Install Docker on each one.

The nodes need two ports to talk to each other:

- `9001` for chunk requests
- `10001` for Raft

Open these ports **only to the other two machines** and your PC, never to the whole internet. Cloud providers call this a firewall or security group.

## Step 3: Use Real Addresses

On one PC, nodes find each other by container name, such as `node2`. Across machines they need real IP addresses. Every node reads the list from `STORAGE_NODES`, and is named after its host, so each node's `NODE_ID` is its own address:

```sh
STORAGE_NODES=http://10.0.0.11:9001,http://10.0.0.12:9001,http://10.0.0.13:9001
```

On each machine, start one storage node. Build the image with `docker build -f storage/Dockerfile -t flick-storage .` from the project folder, then copy it over or push it to a registry:

```sh
docker run -d --restart unless-stopped --network host \
  -v flickdata:/chunks \
  -e NODE_ID=10.0.0.11 -e BOOTSTRAP=true \
  -e STORAGE_NODES=http://10.0.0.11:9001,http://10.0.0.12:9001,http://10.0.0.13:9001 \
  flick-storage
```

- Change `NODE_ID` on each machine, and set `BOOTSTRAP=true` on only the first one.
- `--network host` lets Raft listen on the machine's own address. Inside Docker's own network, that address doesn't exist.
- `--restart unless-stopped` starts the node again by itself after the machine reboots.

Everything else (the API, workers, cache, Nginx, Postgres and Redis) can stay on your PC with Docker Compose:

- Remove `node1`, `node2` and `node3` from `docker-compose.yml`, and from each service's `depends_on`.
- Set `x-storage-nodes` to the same list as `STORAGE_NODES` above.
- Change the `storage` targets in `monitoring/prometheus.yml` to `10.0.0.11:9001`, `10.0.0.12:9001` and `10.0.0.13:9001`.

## Step 4: Chaos with Real Machines

Keep uploading and watching videos the whole time. Then:

1. Switch off one machine completely. Pull the power, or stop the cloud server.
2. Wait a minute. Check that the website still works.
3. Switch it back on. Check that it catches up.
4. Repeat with each of the three machines.

## Done When

- Switching off any one machine leaves the website running and loses no videos.
- The machine that comes back catches up by itself.
