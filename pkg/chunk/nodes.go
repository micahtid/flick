package chunk

// This file keeps track of nodes that can't be reached. Reaching a stopped
// node can take 5 seconds to fail, while Docker looks up its name. So a node
// that fails rests for a while, and the client asks the other nodes first.

import (
	"slices"
	"time"
)

// restTime is how long a node rests after it can't be reached.
const restTime = 10 * time.Second

// rest makes a node rest for the next 10 seconds.
func (c *Client) rest(node string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.restAt[node] = time.Now()
}

// resting reports whether a node couldn't be reached in the last 10 seconds.
func (c *Client) resting(node string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.restAt[node]) < restTime
}

// split divides the nodes into the ones that are ready and the ones that are
// resting.
func (c *Client) split() (ready, resting []string) {
	for _, node := range c.nodes {
		if c.resting(node) {
			resting = append(resting, node)
		} else {
			ready = append(ready, node)
		}
	}
	return ready, resting
}

// order returns every node in the order to ask them: nodes in holders first,
// then the other nodes, and resting nodes last.
func (c *Client) order(holders []string) []string {
	rank := func(node string) int {
		switch {
		case c.resting(node):
			return 2
		case slices.Contains(holders, node):
			return 0
		}
		return 1
	}
	nodes := slices.Clone(c.nodes)
	// A stable sort keeps the nodes in their usual order within each group.
	slices.SortStableFunc(nodes, func(a, b string) int { return rank(a) - rank(b) })
	return nodes
}
