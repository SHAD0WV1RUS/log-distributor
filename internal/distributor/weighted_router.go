package distributor

import (
	"container/list"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
)

// AnalyzerConfig represents analyzer configuration for the tree
type AnalyzerConfig struct {
	AnalyzerID   string
	Weight       float32
	InputChannel chan LogMessage
}

// WeightedTreeNode represents a node in the weight-balanced tree
type WeightedTreeNode struct {
	analyzerID     string
	weight         float32
	leftCumWeight  float32
	rightCumWeight float32
	inputChannel   chan LogMessage
	left           *WeightedTreeNode
	right          *WeightedTreeNode
}

// RouterInterface defines the contract for message routing
type RouterInterface interface {
	RouteMessage(msg LogMessage)
	RegisterAnalyzer(config *AnalyzerConfig)
	UnregisterAnalyzer(config *AnalyzerConfig)
	UpdateWeight(config *AnalyzerConfig, weight float32)
}

type atomicFloat32 struct {
	value atomic.Uint32
}

func (af *atomicFloat32) Load() float32 {
	return math.Float32frombits(af.value.Load())
}

func (af *atomicFloat32) Store(value float32) {
	af.value.Store(math.Float32bits(value))
}

// WeightedTreeRouter implements RouterInterface using a weight-balanced tree
type WeightedTreeRouter struct {
	root         atomic.Pointer[WeightedTreeNode]
	analyzers    *list.List
	totalWeight  atomicFloat32
	rebuildMutex sync.Mutex
}

// NewWeightedTreeRouter creates a new weighted tree router
func NewWeightedTreeRouter() *WeightedTreeRouter {
	return &WeightedTreeRouter{
		analyzers: list.New(),
	}
}

// RouteMessage routes a message using the weight-balanced tree (O(log n))
func (wtr *WeightedTreeRouter) RouteMessage(msg LogMessage) {
	root := wtr.root.Load()
	if root == nil {
		// No analyzers available, drop message
		return
	}

	// Generate random weight sample
	sampleWeight := wtr.totalWeight.Load() * rand.Float32()
	wtr.routeToNode(msg, root, sampleWeight)
}

// routeToNode recursively routes to the appropriate node
func (wtr *WeightedTreeRouter) routeToNode(msg LogMessage, node *WeightedTreeNode, sampleWeight float32) {
	if node == nil {
		return
	}

	// Check if this node should handle the message
	sampleWeight -= node.weight
	if sampleWeight < 0 {
		// Route to this node
		select {
		case node.inputChannel <- msg:
		default:
			// Channel full, try to reroute
			wtr.RouteMessage(msg)
		}
		return
	}

	// Check left subtree
	if sampleWeight < node.leftCumWeight {
		wtr.routeToNode(msg, node.left, sampleWeight)
	} else {
		// Go to right subtree
		wtr.routeToNode(msg, node.right, sampleWeight-node.leftCumWeight)
	}
}

// RegisterAnalyzer adds a new analyzer to the router
func (wtr *WeightedTreeRouter) RegisterAnalyzer(config *AnalyzerConfig) {
	wtr.rebuildMutex.Lock()
	defer wtr.rebuildMutex.Unlock()

	added := false
	var vtreeCopy *WeightedTreeNode = nil
	for e := wtr.analyzers.Front(); e != nil; e = e.Next() {
		curConfig := e.Value.(*AnalyzerConfig)
		if curConfig.Weight < config.Weight {
			vtreeCopy = wtr.addToTree(vtreeCopy, config)
			wtr.analyzers.InsertBefore(config, e)
			added = true
		}
		vtreeCopy = wtr.addToTree(vtreeCopy, curConfig)
	}

	if !added {
		vtreeCopy = wtr.addToTree(vtreeCopy, config)
		wtr.analyzers.PushBack(config)
	}

	wtr.root.Store(vtreeCopy)
	wtr.totalWeight.Store(wtr.totalWeight.Load() + config.Weight)
}

// UnregisterAnalyzer removes an analyzer from the router
func (wtr *WeightedTreeRouter) UnregisterAnalyzer(config *AnalyzerConfig) {
	wtr.rebuildMutex.Lock()
	defer wtr.rebuildMutex.Unlock()

	var vtreeCopy *WeightedTreeNode = nil
	removed := false
	for e := wtr.analyzers.Front(); e != nil; {
		curConfig := e.Value.(*AnalyzerConfig)
		next := e.Next()
		if curConfig.AnalyzerID == config.AnalyzerID {
			wtr.analyzers.Remove(e)
			removed = true
		} else {
			vtreeCopy = wtr.addToTree(vtreeCopy, curConfig)
		}
		e = next
	}

	if removed {
		wtr.totalWeight.Store(wtr.totalWeight.Load() - config.Weight)
		wtr.root.Store(vtreeCopy)
	}
}

// UpdateWeight updates the weight of an existing analyzer
func (wtr *WeightedTreeRouter) UpdateWeight(config *AnalyzerConfig, weight float32) {
	wtr.UnregisterAnalyzer(config)
	config.Weight = weight
	wtr.RegisterAnalyzer(config)
}

func (wtr *WeightedTreeRouter) addToTree(wt *WeightedTreeNode, config *AnalyzerConfig) *WeightedTreeNode {
	if wt == nil {
		return &WeightedTreeNode{
			analyzerID:   config.AnalyzerID,
			weight:       config.Weight,
			inputChannel: config.InputChannel,
		}
	}

	if wt.rightCumWeight < wt.leftCumWeight {
		wt.rightCumWeight += config.Weight
		wt.right = wtr.addToTree(wt.right, config)
	} else {
		wt.leftCumWeight += config.Weight
		wt.left = wtr.addToTree(wt.left, config)
	}

	return wt
}
