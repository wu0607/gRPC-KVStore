package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/ss87021456/gRPC-KVStore/proto"
)

var (
	MAPSIZE int = 32
)

type ServerMgr struct {
	inMemoryCache SharedCache
	lastSnapTime  int64
	prefixLock    sync.RWMutex
}

type SharedCache []*SingleCache

type SingleCache struct {
	lock  sync.RWMutex
	cache map[string]string
}

type JsonData struct {
	Key, Value string
}

func NewServerMgr() *ServerMgr {
	m := make(SharedCache, MAPSIZE)
	for i := 0; i < MAPSIZE; i++ {
		m[i] = &SingleCache{cache: make(map[string]string)}
	}
	return &ServerMgr{inMemoryCache: m}
}

func (s *ServerMgr) Get(ctx context.Context, getReq *pb.GetRequest) (*pb.GetResponse, error) {
	key := getReq.GetKey()
	log.Printf("Get key: %s", key)
	val, err := getHelper(s, key)
	return &pb.GetResponse{Value: val}, err

}

func (s *ServerMgr) Set(ctx context.Context, setReq *pb.SetRequest) (*pb.Empty, error) {
	key, value := setReq.GetKey(), setReq.GetValue()
	log.Printf("Set key: %s, value: %s", key, value)
	writeAheadLog("start", key, value)
	setHelper(s, key, value)
	writeAheadLog("done", key, value)
	return &pb.Empty{}, nil
}

func (s *ServerMgr) GetPrefix(ctx context.Context, getPrefixReq *pb.GetPrefixRequest) (*pb.GetPrefixResponse, error) {
	res := prefixHelper(s, getPrefixReq.GetKey())
	log.Printf("Get prefix: %s", getPrefixReq.GetKey())
	if len(res) > 0 {
		return &pb.GetPrefixResponse{Values: res}, nil
	}
	return &pb.GetPrefixResponse{}, fmt.Errorf("No specific prefix %s found", getPrefixReq.GetKey())
}

func (s *ServerMgr) SnapShot(filename string) {
	oFile, _ := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY, os.ModePerm)
	// clean up the file, but need to use a more efficient way
	oFile.Truncate(0)
	oFile.Seek(0, 0)
	defer oFile.Close()
	encoder := json.NewEncoder(oFile)
	encoder.Encode(time.Now().Unix())
	err := encoder.Encode(s.makeData())
	if err != nil {
		log.Printf("fail to write to file %s", err)
	}
}

func (s *ServerMgr) makeData() interface{} {
	datas := []map[string]interface{}{}
	for i := 0; i < MAPSIZE; i++ {
		for k, v := range s.inMemoryCache[i].cache {
			data := map[string]interface{}{
				"Key":   k,
				"Value": v,
			}
			datas = append(datas, data)
		}
	}
	return datas
}

func (s *ServerMgr) LoadFromSnapshot(filename string) error {
	log.Printf("Initializing cache from file: %s\n", filename)
	iFile, err := os.OpenFile(filename, os.O_RDONLY, os.ModePerm)
	if err != nil {
		log.Println("Need to create new data storage...", err)
		return err
	}
	defer iFile.Close()

	timestampByte := make([]byte, 10) // 10 : unix time length
	iFile.Read(timestampByte)
	timestamp, err := strconv.Atoi(fmt.Sprintf("%s", timestampByte))
	if err != nil {
		log.Printf("Parse timestamp encounter err: %s", err)
	}
	s.lastSnapTime = int64(timestamp)
	iFile.Seek(10, 0)
	decoder := json.NewDecoder(iFile)
	// Read the array open bracket
	if _, err := decoder.Token(); err != nil {
		log.Fatal("Encounter wrong json format data1...", err)
		return err
	}
	// while the array contains values
	for decoder.More() {
		var m JsonData
		err := decoder.Decode(&m)
		if err != nil {
			log.Fatal("Encounter wrong json format data3...", err)
			return err
		}
		setHelper(s, m.Key, m.Value)
	}
	// read closing bracket
	if _, err := decoder.Token(); err != nil {
		log.Fatal("Encounter wrong json format data...", err)
		return err
	}
	log.Printf("Finish initializing cache from file: %s\n", filename)
	return nil
}

func (s *ServerMgr) LoadFromHistoryLog(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		arr := strings.Split(scanner.Text(), ",")

		if arr[3] == "start" {
			fmt.Println("Recover: ", arr[1], arr[2])
			setHelper(s, arr[1], arr[2]) // arr layout -> [timestamp, key, value, mode]
		}
	}
	return nil
}