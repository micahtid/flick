# Phase 9 (Optional): Real Machines

## Why Do This

Until now, all three storage nodes have run on your one PC. Messages between them never really leave the computer, so they arrive almost instantly and never get lost.

Real networks are messier. Messages can be slow, arrive late, or vanish. And a whole computer can lose power, not just one program. This phase proves that Flick still works when the nodes are on truly separate machines.

## Step 1: Practice On A Bad Network At Home

Before using real machines, fake a bad network on your own PC. **Toxiproxy** is a tool that sits between two programs and can slow down or cut their connection on purpose.

Add it to `docker-compose.yml`:

```yaml
toxiproxy:
  image: ghcr.io/shopify/toxiproxy:2.12.0
  ports: ["8474:8474"] # Toxiproxy's control port
```

Make node2's Raft messages go through it:

1. Create a proxy named `node2` that listens on `toxiproxy:20002` and forwards to `node2:10002`:

   ```sh
   curl -X POST localhost:8474/proxies \
     -d '{"name":"node2","listen":"0.0.0.0:20002","upstream":"node2:10002"}'
   ```

2. Change node2's Raft address to `toxiproxy:20002` in the bootstrap list from Phase 5.

Now break things on purpose:

```sh
# Add a 300 ms delay that varies by up to 100 ms either way
curl -X POST localhost:8474/proxies/node2/toxics \
  -d '{"type":"latency","attributes":{"latency":300,"jitter":100}}'

# Cut node2 off completely
curl -X POST localhost:8474/proxies/node2 -d '{"enabled":false}'
```

Watch the Grafana dashboard from Phase 8 while you do this.

**What should happen:** when node2 is cut off, the other two elect a leader if needed and keep working. When node2 is reconnected, it catches up.

**If leaders keep changing again and again,** the delay is making nodes think the leader is dead. Give them more patience by raising `HeartbeatTimeout` and `ElectionTimeout` in the Raft config, for example to 2 seconds each.

## Step 2: Get Three Machines

Any of these work:

- Three small cloud servers, about 1 CPU and 1 GB of memory each.
- Spare laptops or Raspberry Pis on your home network.

Install Docker on each one.

The nodes need two ports to talk to each other:

- `9001` for chunk requests
- `10001` for Raft

Open these ports **only to the other two machines**, never to the whole internet. Cloud providers call this a firewall or security group.

## Step 3: Use Real Addresses

On one PC, nodes found each other by container name, such as `node2`. Across machines they need real IP addresses. Move the addresses into environment variables:

```sh
NODE_ID=node1
ADVERTISE_ADDR=10.0.0.11                            # this machine's own address
PEERS=node1=10.0.0.11,node2=10.0.0.12,node3=10.0.0.13 # every node's address
```

Change the storage node code:

- Build the `BootstrapCluster` server list from `PEERS` instead of writing the names by hand.
- Pass `ADVERTISE_ADDR` to `raft.NewTCPTransport` as its advertise address. This is the address the node tells the others to reach it at. Without it, the node gives its address inside Docker, which the other machines can't reach.
- Change `STORAGE_NODES` on the API and workers to the three real addresses.

On each machine, start one storage node. Build the image from `storage/` and copy it over, or push it to a registry:

```sh
docker run -d --restart unless-stopped \
  -p 9001:9001 -p 10001:10001 -v flickdata:/chunks \
  -e NODE_ID=node1 -e ADVERTISE_ADDR=10.0.0.11 \
  -e PEERS=node1=10.0.0.11,node2=10.0.0.12,node3=10.0.0.13 \
  flick-storage
```

Change `NODE_ID` and `ADVERTISE_ADDR` on each machine. `--restart unless-stopped` starts the node again by itself after the machine reboots.

Everything else (the API, workers, cache, Nginx, Postgres and Redis) can stay on your PC with Docker Compose.

## Step 4: Chaos With Real Machines

Keep uploading and watching videos the whole time. Then:

1. Switch off one machine completely. Pull the power, or stop the cloud server.
2. Wait a minute. Check that the site still works.
3. Switch it back on. Check that it catches up.
4. Repeat with each of the three machines.

## Done When

- Switching off any one machine leaves the site running and loses no videos.
- The machine that comes back catches up by itself.
