package checkpointmanager

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/mihkeltiks/rev-mpi-deb/logger"
	"github.com/mihkeltiks/rev-mpi-deb/rpc"
	"github.com/mihkeltiks/rev-mpi-deb/utils/command"
	"github.com/mihkeltiks/rev-mpi-deb/utils/mpi"
)

type NodeId int

type StorageTier int

const (
	TierL1Only StorageTier = iota
	TierSyncing
	TierL2Available
)

type checkpointRecord struct {
	Id              string
	nodeId          NodeId
	NodeRank        *int
	OpName          string
	IsSend          bool
	CanBeRestored   bool
	parameters      map[string]string
	MatchingEventId *string
	matchingEvent   *checkpointRecord // for send events, a link to the corresponding message receive event, and vice versa
	Tag             *int              // The mpi message tag, if present
	CurrentLocation bool
}

type CheckpointTree struct {
	checkpointLog       CheckpointLog
	parentTree          *CheckpointTree
	childrenCheckpoints []*CheckpointTree
	checkpointDir       string
	l1Dir               string
	l2Dir               string
	tier                StorageTier
	l1Evicted           bool
	commandLog          CommandLog
	counters            []int
	mu                  sync.RWMutex
}

// Data structure for maintaining a list of recorded checkpoints by node
type CheckpointLog map[NodeId][]*checkpointRecord

// Maintaining the state of the checkpointlog at checkpoint
type CheckpointLogList []CheckpointLog

type CommandLog []command.Command

var checkpointLog = make(CheckpointLog)
var checkpointLogList CheckpointLogList

func (cpTree *CheckpointTree) SetTiers(l1Dir, l2Dir string) {
	cpTree.mu.Lock()
	defer cpTree.mu.Unlock()
	cpTree.l1Dir = l1Dir
	cpTree.l2Dir = l2Dir
	cpTree.checkpointDir = l1Dir
	cpTree.tier = TierL1Only
}

func (cpTree *CheckpointTree) MarkTier(tier StorageTier) {
	cpTree.mu.Lock()
	defer cpTree.mu.Unlock()
	cpTree.tier = tier
}

func (cpTree *CheckpointTree) EvictL1() {
	cpTree.mu.Lock()
	defer cpTree.mu.Unlock()
	cpTree.l1Evicted = true
}

func MakeCheckpointTree(cplog CheckpointLog, parentcp *CheckpointTree, childrencps []*CheckpointTree, cpdir string, cmdlog CommandLog, counters []int) *CheckpointTree {
	tree := CheckpointTree{
		checkpointLog:       cplog,
		parentTree:          parentcp,
		childrenCheckpoints: childrencps,
		checkpointDir:       cpdir,
		commandLog:          cmdlog,
		counters:            counters,
		tier:                TierL1Only,
	}

	return &tree
}

func (cpTree *CheckpointTree) AddChildTree(childTree *CheckpointTree) {
	cpTree.childrenCheckpoints = append(cpTree.childrenCheckpoints, childTree)
}

func (cpTree CheckpointTree) GetParentTree() *CheckpointTree {
	return cpTree.parentTree
}

func (cpTree CheckpointTree) GetChildrenTrees() []*CheckpointTree {
	return cpTree.childrenCheckpoints
}

func (cpTree CheckpointTree) GetCounters() []int {
	return cpTree.counters
}

func (cpTree CheckpointTree) HasParent() bool {
	return cpTree.parentTree != nil
}

func (cpTree CheckpointTree) GetCheckpointDir() string {
	cpTree.mu.RLock()
	defer cpTree.mu.RUnlock()

	if !cpTree.l1Evicted && cpTree.l1Dir != "" {
		if _, err := os.Stat(cpTree.l1Dir); err == nil {
			return cpTree.l1Dir
		}
	}
	if cpTree.l2Dir != "" {
		return cpTree.l2Dir
	}
	return cpTree.checkpointDir
}

func (cpTree CheckpointTree) GetCommandlog() *CommandLog {
	return &cpTree.commandLog
}

func (cpTree CheckpointTree) Print() {
	logger.Verbose("cplog %v", cpTree.checkpointLog)
	logger.Verbose("parent %v", cpTree.parentTree)
	logger.Verbose("children %v", cpTree.childrenCheckpoints)
	logger.Verbose("cpdir %v", cpTree.GetCheckpointDir())
	logger.Verbose("commandlog %v", cpTree.commandLog)
}

func GetCheckpointLog() CheckpointLog {
	return checkpointLog
}

func GetCheckpointLogIndex(index int) CheckpointLog {
	return checkpointLogList[index]
}

func AddCheckpointLog() {
	// Create a new map
	newCheckpointLog := make(CheckpointLog)

	// Copy the contents of checkpointLog into the new map
	for k, v := range checkpointLog {
		newCheckpointLog[k] = v
	}

	// Append the new map to checkpointLogList
	checkpointLogList = append(checkpointLogList, newCheckpointLog)
}

func SetCheckpointLog(index int) {
	checkpointLog = make(CheckpointLog)
	// checkpointLog = checkpointLogList[index]
}

var nodeRanks = make(map[NodeId]*int)

func RecordCheckpoint(mpiRecord rpc.MPICallRecord) {
	nodeId := NodeId(mpiRecord.NodeId)
	opName := mpiRecord.OpName

	record := checkpointRecord{
		Id:            mpiRecord.Id,
		nodeId:        nodeId,
		OpName:        opName,
		IsSend:        mpi.SEND_EVENTS[opName],
		CanBeRestored: mpi.RESTORABLE_OPERATIONS[opName],
		parameters:    mpiRecord.Parameters,
	}

	if nodeRanks[nodeId] == nil {
		nodeRanks[nodeId] = tryEvaluateIntegerParam("rank", record)
	}
	record.NodeRank = nodeRanks[nodeId]

	record.Tag = tryEvaluateIntegerParam("tag", record)

	// Link the matching event from other party, if already recorded
	record.findAndLinkMatchingMessage()

	if checkpointLog[nodeId] == nil {
		checkpointLog[nodeId] = make([]*checkpointRecord, 0)
	}

	checkpointLog[nodeId] = append(checkpointLog[nodeId], &record)
}

func findCheckpointById(checkpointId string) *checkpointRecord {
	for _, nodeCheckpoints := range checkpointLog {
		for _, checkpoint := range nodeCheckpoints {
			if checkpoint.Id == checkpointId {
				return checkpoint
			}
		}
	}
	return nil
}

func ListCheckpoints() {
	for nodeId, nodeCheckpoints := range checkpointLog {
		var str string

		for _, record := range nodeCheckpoints {
			str = fmt.Sprintf("%s{%s - %s}", str, record.OpName, record.Id)
			str = fmt.Sprintf("%s,", str)
		}

		logger.Info("Node %d checkpoints:", nodeId)
		logger.Info(str)
	}
}

// Links a corresponding send event to receive events, and vice versa, if found
func (record *checkpointRecord) findAndLinkMatchingMessage() {
	var matchingRecord *checkpointRecord

	switch record.OpName {
	case mpi.MPI_OPS[mpi.OP_SEND]:
		matchingNodeRank, _ := strconv.Atoi(record.parameters["dest"])

		matchingRecord = getFirstUnmatchedMessage(matchingNodeRank, mpi.MPI_OPS[mpi.OP_RECV], record.Tag)

	case mpi.MPI_OPS[mpi.OP_RECV]:
		matchingNodeRank, _ := strconv.Atoi(record.parameters["source"])

		matchingRecord = getFirstUnmatchedMessage(matchingNodeRank, mpi.MPI_OPS[mpi.OP_SEND], record.Tag)
	}

	if matchingRecord != nil {
		logger.Verbose("Linking matching messages  %v:%v - %v:%v", record.nodeId, record.OpName, matchingRecord.nodeId, matchingRecord.OpName)
		record.matchingEvent = matchingRecord
		record.MatchingEventId = &matchingRecord.Id

		matchingRecord.matchingEvent = record
		matchingRecord.MatchingEventId = &record.Id
	}

}

// Finds the first message on a node with specified operation name
func getFirstUnmatchedMessage(nodeRank int, opName string, tag *int) *checkpointRecord {
	var nodeId *NodeId

	for nId, nRank := range nodeRanks {
		if nRank != nil && *nRank == nodeRank {
			nodeId = &nId
			break
		}
	}

	if nodeId == nil {
		return nil
	}

	nodeCheckpoints := checkpointLog[*nodeId]
	if nodeCheckpoints == nil {
		return nil
	}

	for _, checkpoint := range nodeCheckpoints {
		if checkpoint.matchingEvent != nil || checkpoint.CurrentLocation {
			continue
		}
		if checkpoint.OpName == opName && tagsMatch(tag, checkpoint.Tag) {
			return checkpoint
		}
	}
	return nil
}

func tagsMatch(tag1, tag2 *int) bool {
	// tag retrieval has failed, might be false positive
	if tag1 == nil || tag2 == nil {
		return true
	}

	// wildcard tag used
	if *tag1 == -1 || *tag2 == -1 {
		return true
	}

	// matching tags used
	return *tag1 == *tag2
}

func tryEvaluateIntegerParam(paramName string, record checkpointRecord) *int {
	paramStr := record.parameters[paramName]
	if len(paramStr) == 0 {
		return nil
	}

	if value, err := strconv.Atoi(paramStr); err == nil {
		return &value
	}

	return nil
}

func RemoveSubsequentCheckpoints(cpoint checkpointRecord) {
	for nodeIndex, nodeCheckpoints := range checkpointLog {
		for cpIndex, checkpoint := range nodeCheckpoints {
			if checkpoint.Id == cpoint.Id {
				checkpointLog[nodeIndex] = checkpointLog[nodeIndex][:cpIndex+1]
				if cpoint.matchingEvent != nil {
					checkpointLog[nodeIndex][cpIndex].matchingEvent = nil
					checkpointLog[nodeIndex][cpIndex].MatchingEventId = nil
				}
				checkpointLog[nodeIndex][cpIndex].CurrentLocation = true
				return
			}
		}
	}
}

func RemoveCurrentCheckpointMarkersOnNode(nodeId NodeId) {
	for _, checkpoint := range checkpointLog[nodeId] {
		if checkpoint.CurrentLocation {
			checkpoint.CurrentLocation = false
			checkpoint.findAndLinkMatchingMessage()
		}
	}
}

func (c checkpointRecord) String() string {
	return fmt.Sprintf("%v - %v", c.Id, c.OpName)
}

type MigrationTask struct {
	Tree       *CheckpointTree
	L1Path     string
	L2Path     string
	NotifyChan chan error
}

type TierMigrator struct {
	taskQueue   chan MigrationTask
	retentionL1 int
	trackedList []*CheckpointTree
	mu          sync.Mutex
	wg          sync.WaitGroup
	stopChan    chan struct{}
}

func NewTierMigrator(queueSize int, retentionL1 int) *TierMigrator {
	if retentionL1 < 1 {
		retentionL1 = 2 // We retain at least the two most recent checkpoints at L1 to ensure fast incremental checkpointing.
	}
	tm := &TierMigrator{
		taskQueue:   make(chan MigrationTask, queueSize),
		retentionL1: retentionL1,
		trackedList: make([]*CheckpointTree, 0),
		stopChan:    make(chan struct{}),
	}
	tm.startWorker()
	return tm
}

func (tm *TierMigrator) startWorker() {
	tm.wg.Add(1)
	go func() {
		defer tm.wg.Done()
		for {
			select {
			case task := <-tm.taskQueue:
				err := tm.performMigration(task)
				if task.NotifyChan != nil {
					task.NotifyChan <- err
				}
			case <-tm.stopChan:
				return
			}
		}
	}()
}

func (tm *TierMigrator) EnqueueMigration(tree *CheckpointTree, l1Path, l2Path string) {
	tm.mu.Lock()
	if tree != nil {
		tree.MarkTier(TierSyncing)
		tm.trackedList = append(tm.trackedList, tree)
	}
	tm.mu.Unlock()

	tm.taskQueue <- MigrationTask{
		Tree:   tree,
		L1Path: l1Path,
		L2Path: l2Path,
	}
}

func (tm *TierMigrator) performMigration(task MigrationTask) error {
	log.Printf("[MultiLevel] Starting async migration: L1 (%s) -> L2 (%s)\n", task.L1Path, task.L2Path)

	if err := os.MkdirAll(task.L2Path, 0755); err != nil {
		log.Printf("[MultiLevel ERROR] Failed to create L2 dir: %v\n", err)
		return err
	}

	if err := copyDirectory(task.L1Path, task.L2Path); err != nil {
		log.Printf("[MultiLevel ERROR] Copy failed from %s: %v\n", task.L1Path, err)
		return err
	}

	if task.Tree != nil {
		task.Tree.MarkTier(TierL2Available)
	}

	log.Printf("[MultiLevel SUCCESS] Checkpoint safely synced to L2 (%s)\n", task.L2Path)
	tm.enforceRetention()
	return nil
}

func (tm *TierMigrator) enforceRetention() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	for len(tm.trackedList) > tm.retentionL1 {
		oldTree := tm.trackedList[0]
		if oldTree.tier == TierL2Available && oldTree.l1Dir != "" {
			log.Printf("[MultiLevel Retention] Freeing L1 RAM cache: %s\n", oldTree.l1Dir)
			_ = os.RemoveAll(oldTree.l1Dir)
			oldTree.EvictL1()
		}
		tm.trackedList = tm.trackedList[1:]
	}
}

func (tm *TierMigrator) Close() {
	close(tm.stopChan)
	tm.wg.Wait()
}

func copyDirectory(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.Type()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(srcPath)
			if err != nil {
				return err
			}
			_ = os.Remove(dstPath)
			if err := os.Symlink(linkTarget, dstPath); err != nil {
				return err
			}
			continue
		}

		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0755); err != nil {
				return err
			}
			if err := copyDirectory(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
