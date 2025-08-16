package distributor

import (
	"container/list"
	"log"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// AnalyzerConfig represents analyzer configuration for the tree
type AnalyzerConfig struct {
	AnalyzerID      string
	Weight          float32
	InputChannels   [256]chan LogMessage  // Priority channels (0 = highest priority)
}

// WeightedTreeNode represents a node in the weight-balanced tree
type WeightedTreeNode struct {
	analyzerID     string
	weight         float32
	leftCumWeight  float32
	rightCumWeight float32
	inputChannels  [256]chan LogMessage  // Priority channels (0 = highest priority)
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
	const maxAttempts = 20
	baseBackoff := time.Microsecond * 10 // Start with 10μs
	
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		curNode := wtr.root.Load()
		if curNode == nil {
			// No analyzers available, apply backoff before retry
			backoffTime := time.Duration(attempt) * baseBackoff
			time.Sleep(backoffTime)
			continue
		}
		sampleWeight := wtr.totalWeight.Load() * rand.Float32()
		for curNode != nil {
			sampleWeight -= curNode.weight
			if sampleWeight < 0 {
				// Route to this node using priority channel
				priority := msg.GetPriority()
				select {
				case curNode.inputChannels[priority] <- msg:
					return
				default:
					break
				}
			}
			if sampleWeight < curNode.leftCumWeight {
				curNode = curNode.left
			} else {
				sampleWeight -= curNode.leftCumWeight
				curNode = curNode.right
			}
		}
		
		// Apply exponential backoff before retry (except on last attempt)
		backoffTime := time.Duration(attempt) * baseBackoff
		time.Sleep(backoffTime)
	}
	// Log dropped message after all retries failed
	log.Printf("WARNING: Message dropped after %d routing attempts - all channels full or no analyzers available", maxAttempts)
}

// RegisterAnalyzer adds a new analyzer to the router
func (wtr *WeightedTreeRouter) RegisterAnalyzer(config *AnalyzerConfig) {
	wtr.rebuildMutex.Lock()
	defer wtr.rebuildMutex.Unlock()

	added := false
	var vtreeCopy *WeightedTreeNode = nil
	for e := wtr.analyzers.Front(); e != nil; e = e.Next() {
		curConfig := e.Value.(*AnalyzerConfig)
		if !added && curConfig.Weight < config.Weight {
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
			analyzerID:    config.AnalyzerID,
			weight:        config.Weight,
			inputChannels: config.InputChannels,
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