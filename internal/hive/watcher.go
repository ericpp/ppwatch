package hive

import (
	"context"
	"log"
	"time"
)

// opIDRegex matches podping operation ids: any id starting with "pp_" that
// contains a second underscore, or the exact id "podping".
// (Defined in blockparse.go.)

// Watcher watches for podcast update notifications on the Hive blockchain.
type Watcher struct {
	Nodes []string

	running      bool
	totalUpdates int
	currentBlock *int64

	callback func(PodpingData)
}

// NewWatcher creates a watcher.
func NewWatcher(nodes []string) *Watcher {
	return &Watcher{Nodes: nodes}
}

// OnUpdate registers the callback that receives PodpingData.
func (w *Watcher) OnUpdate(cb func(PodpingData)) {
	w.callback = cb
}

// Start watches for podcast updates. It runs until Stop is called or the
// context is cancelled.
func (w *Watcher) Start(ctx context.Context) error {
	if w.running {
		return NewError("Watcher is already running")
	}
	w.running = true
	defer func() { w.running = false }()

	client := NewHiveClient(w.Nodes)

	for w.running {
		w.processBlocks(ctx, client)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(3 * time.Second):
		}
	}
	return nil
}

// Stop stops the watcher.
func (w *Watcher) Stop() {
	w.running = false
}

// Running reports whether the watcher loop is active.
func (w *Watcher) Running() bool { return w.running }

// TotalUpdates returns the number of updates processed so far.
func (w *Watcher) TotalUpdates() int { return w.totalUpdates }

// CurrentBlock returns the block currently being processed, if any.
func (w *Watcher) CurrentBlock() (int64, bool) {
	if w.currentBlock == nil {
		return 0, false
	}
	return *w.currentBlock, true
}

func (w *Watcher) processBlocks(ctx context.Context, client *HiveClient) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Error processing blocks: %v", r)
		}
	}()

	headBlock, ok := w.headBlock(ctx, client)
	if !ok {
		return
	}

	if w.currentBlock == nil {
		log.Printf("Starting from block %d", headBlock)
		b := headBlock
		w.currentBlock = &b
	}

	for *w.currentBlock <= headBlock && w.running {
		log.Printf("Processing block  %d", *w.currentBlock)

		block, ok := w.getBlock(ctx, client, *w.currentBlock)
		if !ok {
			return
		}

		updates := w.processBlock(block, *w.currentBlock)
		w.totalUpdates += updates
		*w.currentBlock++
	}
}

func (w *Watcher) headBlock(ctx context.Context, client *HiveClient) (int64, bool) {
	props, err := client.GetDynamicGlobalProperties(ctx)
	if err != nil {
		// Malformed responses are reported the same way as network failures.
		log.Printf("Failed to get head block: %v", err)
		return 0, false
	}
	head, ok := toInt64(props["head_block_number"])
	if !ok {
		log.Printf("Malformed head block response: missing head_block_number")
		return 0, false
	}
	return head, true
}

func (w *Watcher) getBlock(ctx context.Context, client *HiveClient, blockNum int64) (map[string]any, bool) {
	block, err := client.GetBlock(ctx, blockNum)
	if err != nil {
		log.Printf("Failed to get block %d: %v", blockNum, err)
		return nil, false
	}
	if block == nil {
		log.Printf("get_block %d returned null", blockNum)
		return nil, false
	}
	return block, true
}
