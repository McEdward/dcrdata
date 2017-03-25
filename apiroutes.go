package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	apitypes "github.com/dcrdata/dcrdata/dcrdataapi"
	"github.com/decred/dcrd/dcrjson"
	"github.com/decred/dcrrpcclient"
)

type APIDataSource interface {
	GetHeight() int
	//Get(idx int) *blockdata.BlockData
	GetHeader(idx int) *dcrjson.GetBlockHeaderVerboseResult
	GetFeeInfo(idx int) *dcrjson.FeeInfoBlock
	//GetStakeDiffEstimate(idx int) *dcrjson.EstimateStakeDiffResult
	GetStakeInfoExtended(idx int) *apitypes.StakeInfoExtended
	GetStakeDiffEstimates() *apitypes.StakeDiff
	//GetBestBlock() *blockdata.BlockData
	GetSummary(idx int) *apitypes.BlockDataBasic
	GetBestBlockSummary() *apitypes.BlockDataBasic
	GetPoolInfo(idx int) *apitypes.TicketPoolInfo
	GetPoolInfoRange(idx0, idx1 int) []apitypes.TicketPoolInfo
	GetPoolValAndSizeRange(idx0, idx1 int) ([]float64, []float64)
	GetSDiff(idx int) float64
	GetSDiffRange(idx0, idx1 int) []float64
	GetMempoolSSTxSummary() *apitypes.MempoolTicketFeeInfo
	GetMempoolSSTxFeeRates(N int) *apitypes.MempoolTicketFees
	GetMempoolSSTxDetails(N int) *apitypes.MempoolTicketDetails
}

// dcrdata application context used by all route handlers
type appContext struct {
	nodeClient *dcrrpcclient.Client
	BlockData  APIDataSource
	Status     apitypes.Status
}

// Constructor for appContext
func newContext(client *dcrrpcclient.Client, blockData APIDataSource) *appContext {
	conns, _ := client.GetConnectionCount()
	nodeHeight, _ := client.GetBlockCount()
	return &appContext{
		nodeClient: client,
		BlockData:  blockData,
		Status: apitypes.Status{
			Height:          uint32(nodeHeight),
			NodeConnections: conns,
			APIVersion:      APIVersion,
			DcrdataVersion:  ver.String(),
		},
	}
}

func (c *appContext) StatusNtfnHandler(wg *sync.WaitGroup, quit chan struct{}) {
	defer wg.Done()
out:
	for {
	keepon:
		select {
		case height, ok := <-ntfnChans.updateStatusNodeHeight:
			if !ok {
				log.Warnf("Block connected channel closed.")
				break out
			}

			c.Status.Height = height

			var err error
			c.Status.NodeConnections, err = c.nodeClient.GetConnectionCount()
			if err != nil {
				c.Status.Ready = false
				log.Warn("Failed to get connection count: ", err)
				break keepon
			}

		case height, ok := <-ntfnChans.updateStatusDBHeight:
			if !ok {
				log.Warnf("Block connected channel closed.")
				break out
			}

			if c.BlockData == nil {
				panic("BlockData APIDataSource is nil")
			}

			summary := c.BlockData.GetBestBlockSummary()
			if summary == nil {
				log.Errorf("BlockData summary is nil")
				break keepon
			}

			bdHeight := c.BlockData.GetHeight()
			if bdHeight >= 0 && summary.Height == uint32(bdHeight) &&
				height == uint32(bdHeight) {
				c.Status.DBHeight = height
				// if DB height agrees with node height, then we're ready
				if c.Status.Height == height {
					c.Status.Ready = true
				} else {
					c.Status.Ready = false
				}
				break keepon
			}

			c.Status.Ready = false
			log.Errorf("New DB height (%d) and stored block data (%d, %d) not consistent.",
				height, bdHeight, summary.Height)

		case _, ok := <-quit:
			if !ok {
				log.Debugf("Got quit signal. Exiting block connected handler for STATUS monitor.")
				break out
			}
		}
	}
}

// root is a http.Handler intended for the API root path. This essentially
// provides a heartbeat, and no information about the application status.
func (c *appContext) root(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "dcrdata api running")
}

func getBlockIndexCtx(r *http.Request) int {
	idx, ok := r.Context().Value(ctxBlockIndex).(int)
	if !ok {
		apiLog.Error("block index not set")
		return -1
	}
	return idx
}

func getBlockIndex0Ctx(r *http.Request) int {
	idx, ok := r.Context().Value(ctxBlockIndex0).(int)
	if !ok {
		apiLog.Error("block index0 not set")
		return -1
	}
	return idx
}

func getNCtx(r *http.Request) int {
	N, ok := r.Context().Value(ctxN).(int)
	if !ok {
		apiLog.Trace("N not set")
		return -1
	}
	return N
}

func getStatusCtx(r *http.Request) *apitypes.Status {
	status, ok := r.Context().Value(ctxAPIStatus).(*apitypes.Status)
	if !ok {
		apiLog.Error("apitypes.Status not set")
		return nil
	}
	return status
}

func writeJSONHandlerFunc(thing interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, thing)
	}
}

func writeJSON(w http.ResponseWriter, thing interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(thing); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) status(w http.ResponseWriter, r *http.Request) {
	// status := getStatusCtx(r)
	// if status == nil {
	// 	http.Error(w, http.StatusText(422), 422)
	// 	return
	// }

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(c.Status); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) currentHeight(w http.ResponseWriter, r *http.Request) {
	// status := getStatusCtx(r)
	// if status == nil {
	// 	http.Error(w, http.StatusText(422), 422)
	// 	return
	// }

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := io.WriteString(w, strconv.Itoa(int(c.Status.Height))); err != nil {
		apiLog.Infof("failed to write height response: %v", err)
	}
}

func (c *appContext) getLatestBlock(w http.ResponseWriter, r *http.Request) {
	latestBlockSummary := c.BlockData.GetBestBlockSummary()
	if latestBlockSummary == nil {
		apiLog.Error("Unable to get latest block summary")
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(latestBlockSummary); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getBlockSummary(w http.ResponseWriter, r *http.Request) {
	idx := getBlockIndexCtx(r)
	if idx < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	blockSummary := c.BlockData.GetSummary(idx)
	if blockSummary == nil {
		apiLog.Errorf("Unable to get block %d summary", idx)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(blockSummary); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getBlockHeader(w http.ResponseWriter, r *http.Request) {
	idx := getBlockIndexCtx(r)
	if idx < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	blockHeader := c.BlockData.GetHeader(idx)
	if blockHeader == nil {
		apiLog.Errorf("Unable to get block %d header", idx)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(blockHeader); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getBlockFeeInfo(w http.ResponseWriter, r *http.Request) {
	idx := getBlockIndexCtx(r)
	if idx < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	blockFeeInfo := c.BlockData.GetFeeInfo(idx)
	if blockFeeInfo == nil {
		apiLog.Errorf("Unable to get block %d fee info", idx)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(blockFeeInfo); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getBlockStakeInfoExtended(w http.ResponseWriter, r *http.Request) {
	idx := getBlockIndexCtx(r)
	if idx < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	stakeinfo := c.BlockData.GetStakeInfoExtended(idx)
	if stakeinfo == nil {
		apiLog.Errorf("Unable to get block %d fee info", idx)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(stakeinfo); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getStakeDiffSummary(w http.ResponseWriter, r *http.Request) {
	stakeDiff := c.BlockData.GetStakeDiffEstimates()
	if stakeDiff == nil {
		apiLog.Errorf("Unable to get stake diff info")
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(stakeDiff); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getStakeDiffCurrent(w http.ResponseWriter, r *http.Request) {
	stakeDiff := c.BlockData.GetStakeDiffEstimates()
	if stakeDiff == nil {
		apiLog.Errorf("Unable to get stake diff info")
	}

	stakeDiffCurrent := dcrjson.GetStakeDifficultyResult{
		CurrentStakeDifficulty: stakeDiff.CurrentStakeDifficulty,
		NextStakeDifficulty:    stakeDiff.NextStakeDifficulty,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(stakeDiffCurrent); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getStakeDiffEstimates(w http.ResponseWriter, r *http.Request) {
	stakeDiff := c.BlockData.GetStakeDiffEstimates()
	if stakeDiff == nil {
		apiLog.Errorf("Unable to get stake diff info")
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(stakeDiff.Estimates); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getSSTxSummary(w http.ResponseWriter, r *http.Request) {
	sstxSummary := c.BlockData.GetMempoolSSTxSummary()
	if sstxSummary == nil {
		apiLog.Errorf("Unable to get SSTx info from mempool")
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(sstxSummary); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getSSTxFees(w http.ResponseWriter, r *http.Request) {
	N := getNCtx(r)
	sstxFees := c.BlockData.GetMempoolSSTxFeeRates(N)
	if sstxFees == nil {
		apiLog.Errorf("Unable to get SSTx fees from mempool")
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(sstxFees); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getSSTxDetails(w http.ResponseWriter, r *http.Request) {
	N := getNCtx(r)
	sstxDetails := c.BlockData.GetMempoolSSTxDetails(N)
	if sstxDetails == nil {
		apiLog.Errorf("Unable to get SSTx details from mempool")
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(sstxDetails); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getBlockRangeSummary(w http.ResponseWriter, r *http.Request) {
	idx0 := getBlockIndex0Ctx(r)
	if idx0 < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	idx := getBlockIndexCtx(r)
	if idx < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	// TODO: check that we have all in range

	N := idx - idx0 + 1
	summaries := make([]*apitypes.BlockDataBasic, 0, N)
	for i := idx0; i <= idx; i++ {
		summaries = append(summaries, c.BlockData.GetSummary(i))
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(summaries); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}

	// DEBUGGING
	// msg := fmt.Sprintf("block range: %d to %d", idx0, idx)

	// w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	// if _, err := io.WriteString(w, msg); err != nil {
	// 	apiLog.Infof("failed to write response: %v", err)
	// }
}

func (c *appContext) getTicketPoolInfo(w http.ResponseWriter, r *http.Request) {
	idx := getBlockIndexCtx(r)
	if idx < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	tpi := c.BlockData.GetPoolInfo(idx)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(tpi); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getTicketPoolInfoRange(w http.ResponseWriter, r *http.Request) {
	idx0 := getBlockIndex0Ctx(r)
	if idx0 < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	idx := getBlockIndexCtx(r)
	if idx < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	useArray := r.URL.Query().Get("arrays")
	if useArray == "1" || useArray == "true" {
		c.getTicketPoolValAndSizeRange(w, r)
		return
	}

	tpis := c.BlockData.GetPoolInfoRange(idx0, idx)
	if tpis == nil {
		http.Error(w, "invalid range", http.StatusUnprocessableEntity)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(tpis); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getTicketPoolValAndSizeRange(w http.ResponseWriter, r *http.Request) {
	idx0 := getBlockIndex0Ctx(r)
	if idx0 < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	idx := getBlockIndexCtx(r)
	if idx < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	pvs, pss := c.BlockData.GetPoolValAndSizeRange(idx0, idx)
	if pvs == nil || pss == nil {
		http.Error(w, "invalid range", http.StatusUnprocessableEntity)
		return
	}

	tPVS := apitypes.TicketPoolValsAndSizes{
		StartHeight: uint32(idx0),
		EndHeight:   uint32(idx),
		Value:       pvs,
		Size:        pss,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(tPVS); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getStakeDiff(w http.ResponseWriter, r *http.Request) {
	idx := getBlockIndexCtx(r)
	if idx < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	sdiff := c.BlockData.GetSDiff(idx)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode([]float64{sdiff}); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}

func (c *appContext) getStakeDiffRange(w http.ResponseWriter, r *http.Request) {
	idx0 := getBlockIndex0Ctx(r)
	if idx0 < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	idx := getBlockIndexCtx(r)
	if idx < 0 {
		http.Error(w, http.StatusText(422), 422)
		return
	}

	sdiffs := c.BlockData.GetSDiffRange(idx0, idx)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(sdiffs); err != nil {
		apiLog.Infof("JSON encode error: %v", err)
	}
}
