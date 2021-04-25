package rpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MiningPool0826/xmrpool/pool"
)

type RPCClient struct {
	sync.RWMutex
	sickRate         int64
	successRate      int64
	Accepts          int64
	Rejects          int64
	LastSubmissionAt int64
	FailsCount       int64
	Url              *url.URL
	//login            string
	//password         string
	Name   string
	sick   bool
	client *http.Client
	info   atomic.Value
}

type GetBlockTemplateReply struct {
	Difficulty     int64  `json:"difficulty"`
	Height         int64  `json:"height"`
	Blob           string `json:"blocktemplate_blob"`
	ReservedOffset int    `json:"reserved_offset"`
	PrevHash       string `json:"prev_hash"`

	ExpectedReward int64 `json:"expected_reward"`
}

type GetInfoReply struct {
	IncomingConnections int64  `json:"incoming_connections_count"`
	OutgoingConnections int64  `json:"outgoing_connections_count"`
	Height              int64  `json:"height"`
	TxPoolSize          int64  `json:"tx_pool_size"`
	Status              string `json:"status"`
}

type BlockHeader struct {
	BlockSize    int    `json:"block_size"`
	Depth        int    `json:"depth"`
	Difficulty   int64  `json:"difficulty"`
	Hash         string `json:"hash"`
	Height       int    `json:"height"`
	MajorVersion int    `json:"major_version"`
	MinorVersion int    `json:"minor_version"`
	Nonce        uint32 `json:"nonce"`
	NumTxes      int    `json:"num_txes"`
	OrphanStatus bool   `json:"orphan_status"`
	PrevHash     string `json:"prev_hash"`
	Reward       int64  `json:"reward"`
	Timestamp    uint32 `json:"timestamp"`
}

type GetBlockHeaderReply struct {
	BlockHeader BlockHeader `json:"block_header"`
	Status      string      `json:"status"`
	Untrusted   bool        `json:"untrusted"`
}

type GetBlockCountReply struct {
	Count  int64  `json:"count"`
	Status string `json:"status"`
}

type JSONRpcResp struct {
	Id     *json.RawMessage       `json:"id"`
	Result *json.RawMessage       `json:"result"`
	Error  map[string]interface{} `json:"error"`
}

func NewRPCClient(cfg *pool.Upstream) (*RPCClient, error) {
	rawUrl := fmt.Sprintf("http://%s:%v/json_rpc", cfg.Host, cfg.Port)
	url, err := url.Parse(rawUrl)
	if err != nil {
		return nil, err
	}
	rpcClient := &RPCClient{Name: cfg.Name, Url: url}
	timeout, _ := time.ParseDuration(cfg.Timeout)
	rpcClient.SetClient(&http.Client{
		Timeout: timeout,
	})
	return rpcClient, nil
}

func (r *RPCClient) SetClient(client *http.Client) {
	r.client = client
}

func (r *RPCClient) GetBlockTemplate(reserveSize int, address string) (*GetBlockTemplateReply, error) {
	params := map[string]interface{}{"reserve_size": reserveSize, "wallet_address": address}
	rpcResp, err := r.doPost(r.Url.String(), "getblocktemplate", params)
	var reply *GetBlockTemplateReply
	if err != nil {
		return nil, err
	}
	if rpcResp.Result != nil {
		err = json.Unmarshal(*rpcResp.Result, &reply)
	}
	return reply, err
}

func (r *RPCClient) GetInfo() (*GetInfoReply, error) {
	params := make(map[string]interface{})
	rpcResp, err := r.doPost(r.Url.String(), "get_info", params)
	var reply *GetInfoReply
	if err != nil {
		return nil, err
	}
	if rpcResp.Result != nil {
		err = json.Unmarshal(*rpcResp.Result, &reply)
	}
	return reply, err
}

func (r *RPCClient) GetBlockCount() (*GetBlockCountReply, error) {
	params := make(map[string]interface{})
	rpcResp, err := r.doPost(r.Url.String(), "getblockcount", params)
	var reply *GetBlockCountReply
	if err != nil {
		return nil, err
	}
	if rpcResp.Result != nil {
		err = json.Unmarshal(*rpcResp.Result, &reply)
	}
	return reply, err
}

func (r *RPCClient) SubmitBlock(hash string) (*JSONRpcResp, error) {
	return r.doPost(r.Url.String(), "submitblock", []string{hash})
}

func (r *RPCClient) GetBlockHeaderByHeight(height int64) (*GetBlockHeaderReply, error) {
	params := map[string]interface{}{"height": height}
	rpcResp, err := r.doPost(r.Url.String(), "getblockheaderbyheight", params)
	var reply *GetBlockHeaderReply
	if err != nil {
		return nil, err
	}
	if rpcResp.Result != nil {
		err = json.Unmarshal(*rpcResp.Result, &reply)
	}
	return reply, err
}

func (r *RPCClient) doPost(url, method string, params interface{}) (*JSONRpcResp, error) {
	jsonReq := map[string]interface{}{"jsonrpc": "2.0", "id": 0, "method": method, "params": params}
	data, _ := json.Marshal(jsonReq)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	req.Header.Set("Content-Length", (string)(len(data)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	//req.SetBasicAuth(r.login, r.password)
	resp, err := r.client.Do(req)
	if err != nil {
		r.markSick()
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, errors.New(resp.Status)
	}

	var rpcResp *JSONRpcResp
	err = json.NewDecoder(resp.Body).Decode(&rpcResp)
	if err != nil {
		r.markSick()
		return nil, err
	}
	if rpcResp.Error != nil {
		r.markSick()
		return nil, errors.New(rpcResp.Error["message"].(string))
	}
	return rpcResp, err
}

func (r *RPCClient) Check(reserveSize int, address string) (bool, error) {
	_, err := r.GetBlockTemplate(reserveSize, address)
	if err != nil {
		return false, err
	}
	r.markAlive()
	return !r.Sick(), nil
}

func (r *RPCClient) Sick() bool {
	r.RLock()
	defer r.RUnlock()
	return r.sick
}

func (r *RPCClient) markSick() {
	r.Lock()
	if !r.sick {
		atomic.AddInt64(&r.FailsCount, 1)
	}
	r.sickRate++
	r.successRate = 0
	if r.sickRate >= 5 {
		r.sick = true
	}
	r.Unlock()
}

func (r *RPCClient) markAlive() {
	r.Lock()
	r.successRate++
	if r.successRate >= 5 {
		r.sick = false
		r.sickRate = 0
		r.successRate = 0
	}
	r.Unlock()
}

func (r *RPCClient) UpdateInfo() (*GetInfoReply, error) {
	info, err := r.GetInfo()
	if err == nil {
		r.info.Store(info)
	}
	return info, err
}

func (r *RPCClient) Info() *GetInfoReply {
	reply, _ := r.info.Load().(*GetInfoReply)
	return reply
}
