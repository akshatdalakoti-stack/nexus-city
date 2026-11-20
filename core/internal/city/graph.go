package city

import (
	"container/heap"
	"math"
)

// BuildAdjacency (re)builds the node → outgoing links table. Generate calls
// it; call it again after decoding a City from JSON.
func (c *City) BuildAdjacency() {
	c.adj = make([][]Link, len(c.Nodes))
	for i, r := range c.Roads {
		c.adj[r.A] = append(c.adj[r.A], Link{Road: i, To: r.B, Dir: 0})
		c.adj[r.B] = append(c.adj[r.B], Link{Road: i, To: r.A, Dir: 1})
	}
}

// Links returns the ways out of node n.
func (c *City) Links(n int) []Link { return c.adj[n] }

// Connected reports whether every intersection can reach every other.
func (c *City) Connected() bool {
	if len(c.Nodes) == 0 {
		return true
	}
	seen := make([]bool, len(c.Nodes))
	stack := []int{0}
	seen[0] = true
	count := 1
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, l := range c.adj[u] {
			if !seen[l.To] {
				seen[l.To] = true
				count++
				stack = append(stack, l.To)
			}
		}
	}
	return count == len(c.Nodes)
}

// CostFn gives the cost (seconds) of traversing a road; PenaltyFn gives the
// expected delay (seconds) of passing through an intersection. A road cost
// of +Inf means the road is closed.
type (
	CostFn    func(road int) float64
	PenaltyFn func(node int) float64
)

type pqItem struct {
	node int
	dist float64
}

type pq []pqItem

func (q pq) Len() int { return len(q) }
func (q pq) Less(i, j int) bool {
	if q[i].dist != q[j].dist {
		return q[i].dist < q[j].dist
	}
	return q[i].node < q[j].node // deterministic tie-break
}
func (q pq) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *pq) Push(x any)   { *q = append(*q, x.(pqItem)) }
func (q *pq) Pop() any {
	old := *q
	it := old[len(old)-1]
	*q = old[:len(old)-1]
	return it
}

// dijkstra returns the distance to every node and the link used to reach it.
func (c *City) dijkstra(src int, stopAt int, cost CostFn, penalty PenaltyFn) ([]float64, []Link) {
	n := len(c.Nodes)
	dist := make([]float64, n)
	prev := make([]Link, n)
	for i := range dist {
		dist[i] = math.Inf(1)
		prev[i] = Link{Road: -1}
	}
	dist[src] = 0
	q := &pq{{node: src}}
	for q.Len() > 0 {
		it := heap.Pop(q).(pqItem)
		if it.dist > dist[it.node] {
			continue
		}
		if it.node == stopAt {
			break
		}
		for _, l := range c.adj[it.node] {
			w := cost(l.Road)
			if math.IsInf(w, 1) {
				continue
			}
			if penalty != nil && l.To != stopAt {
				w += penalty(l.To)
			}
			nd := it.dist + w
			if nd < dist[l.To] {
				dist[l.To] = nd
				prev[l.To] = Link{Road: l.Road, To: it.node, Dir: l.Dir} // To holds the predecessor here
				heap.Push(q, pqItem{node: l.To, dist: nd})
			}
		}
	}
	return dist, prev
}

// ShortestPath returns the sequence of links from src to dst minimising
// total cost, the cost itself, and whether dst is reachable.
func (c *City) ShortestPath(src, dst int, cost CostFn, penalty PenaltyFn) ([]Link, float64, bool) {
	if src == dst {
		return nil, 0, true
	}
	dist, prev := c.dijkstra(src, dst, cost, penalty)
	if math.IsInf(dist[dst], 1) {
		return nil, 0, false
	}
	var rev []Link
	for at := dst; at != src; {
		p := prev[at]
		rev = append(rev, Link{Road: p.Road, To: at, Dir: p.Dir})
		at = p.To
	}
	path := make([]Link, len(rev))
	for i := range rev {
		path[i] = rev[len(rev)-1-i]
	}
	return path, dist[dst], true
}

// TravelTimes returns the cost from src to every node.
func (c *City) TravelTimes(src int, cost CostFn, penalty PenaltyFn) []float64 {
	dist, _ := c.dijkstra(src, -1, cost, penalty)
	return dist
}
