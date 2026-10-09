package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	concurrency := flag.Int("c", 50, "并发协程数 (Concurrent Workers)")
	totalRequests := flag.Int("n", 2000, "总请求数 (Total Requests)")
	targetURL := flag.String("url", "http://localhost:8080", "目标服务地址")
	flag.Parse()

	log.Printf("🔥 压测准备启动: 目标=%s, 并发协程数=%d, 总请求数=%d\n", *targetURL, *concurrency, *totalRequests)

	// 高并发专用的 HTTP Client（连接池调优，防止端口耗尽）
	// Go 核心考点：必须调大 MaxIdleConnsPerHost，默认值只有 2！
	client := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        500,
			MaxIdleConnsPerHost: *concurrency * 2,
			IdleConnTimeout:     30 * time.Second,
		},
		Timeout: 10 * time.Second,
	}

	sessionID, err := createTestSession(client, *targetURL)
	if err != nil {
		log.Fatalf("创建压测 Session 失败: %v", err)
	}
	log.Printf("✅ 成功创建压测 Session: %s，开始高并发轰炸！\n", sessionID)

	messageURL := fmt.Sprintf("%s/api/sessions/%s/messages", *targetURL, sessionID)

	var successCount int64
	var failCount int64
	var mu sync.Mutex
	latencies := make([]time.Duration, 0, *totalRequests)

	jobs := make(chan int, *totalRequests)
	for i := 0; i < *totalRequests; i++ {
		jobs <- i
	}
	close(jobs)

	startTime := time.Now()
	var wg sync.WaitGroup

	for w := 0; w < *concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			payload := []byte(`{"role":"user","content":"高并发测试消息: 提取关键帧与总结"}`)

			for range jobs {
				reqStart := time.Now()
				req, _ := http.NewRequest("POST", messageURL, bytes.NewBuffer(payload))
				req.Header.Set("Content-Type", "application/json")

				resp, err := client.Do(req)
				latency := time.Since(reqStart)

				if err == nil && resp.StatusCode == http.StatusCreated {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					atomic.AddInt64(&successCount, 1)

					mu.Lock()
					latencies = append(latencies, latency)
					mu.Unlock()
				} else {
					if resp != nil {
						_ = resp.Body.Close()
					}
					atomic.AddInt64(&failCount, 1)
				}
			}
		}(w)
	}

	wg.Wait()
	totalDuration := time.Since(startTime)

	printReport(totalDuration, *totalRequests, successCount, failCount, latencies)
}

func createTestSession(client *http.Client, baseURL string) (string, error) {
	payload := []byte(`{"user_id":"bench_tester","title":"High Concurrency Test"}`)
	resp, err := client.Post(baseURL+"/api/sessions", "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.ID, nil
}

func printReport(duration time.Duration, total int, success int64, fail int64, latencies []time.Duration) {
	sort.Slice(latencies, func(i, j int) bool {
		return latencies[i] < latencies[j]
	})

	qps := float64(success) / duration.Seconds()

	var p50, p95, p99, avg time.Duration
	if len(latencies) > 0 {
		var sum time.Duration
		for _, l := range latencies {
			sum += l
		}
		avg = sum / time.Duration(len(latencies))
		p50 = latencies[len(latencies)*50/100]
		p95 = latencies[len(latencies)*95/100]
		p99 = latencies[len(latencies)*99/100]
	}

	fmt.Println("\n================= 📊 压测结果报告 =================")
	fmt.Printf("总耗时 (Total Time):     %v\n", duration)
	fmt.Printf("成功请求 (Success):       %d\n", success)
	fmt.Printf("失败请求 (Failed):        %d\n", fail)
	fmt.Printf("实际吞吐量 (QPS):         %.2f req/sec\n", qps)
	fmt.Printf("平均延迟 (Avg Latency):   %v\n", avg)
	fmt.Printf("P50 延迟 (中位数):        %v\n", p50)
	fmt.Printf("P95 延迟 (95分位):        %v\n", p95)
	fmt.Printf("P99 延迟 (99分位):        %v\n", p99)
	fmt.Println("==================================================")
}
