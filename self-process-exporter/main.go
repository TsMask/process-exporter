package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/shirou/gopsutil/v4/process"
)

// CachedProcess 包装进程对象和预先获取的静态信息（如名称）
// 避免每次采集都去读 /proc/pid/comm
type CachedProcess struct {
	Proc *process.Process
	Name string
}

type ProcessCollector struct {
	targetNames []string

	// 缓存相关
	cachedProcs map[int32]CachedProcess // PID -> Process 映射
	rwMutex     sync.RWMutex            // 读写锁保护 cachedProcs

	// 指标描述符
	up, cpuUser, cpuSystem, memoryRSS, memoryVMS, numThreads, openFDs, startTime *prometheus.Desc
}

func NewProcessCollector(names []string) *ProcessCollector {
	return &ProcessCollector{
		targetNames: names,
		cachedProcs: make(map[int32]CachedProcess),
		up: prometheus.NewDesc(
			"process_up", "Whether the process is running (1) or not (0).",
			[]string{"process_name", "pid"}, nil,
		),
		cpuUser: prometheus.NewDesc(
			"process_cpu_user_seconds_total", "Total user CPU time spent in seconds.",
			[]string{"process_name", "pid"}, nil,
		),
		cpuSystem: prometheus.NewDesc(
			"process_cpu_system_seconds_total", "Total system CPU time spent in seconds.",
			[]string{"process_name", "pid"}, nil,
		),
		memoryRSS: prometheus.NewDesc(
			"process_memory_rss_bytes", "Resident memory size in bytes.",
			[]string{"process_name", "pid"}, nil,
		),
		memoryVMS: prometheus.NewDesc(
			"process_memory_vms_bytes", "Virtual memory size in bytes.",
			[]string{"process_name", "pid"}, nil,
		),
		numThreads: prometheus.NewDesc(
			"process_num_threads", "Total number of threads.",
			[]string{"process_name", "pid"}, nil,
		),
		openFDs: prometheus.NewDesc(
			"process_open_fds", "Number of open file descriptors.",
			[]string{"process_name", "pid"}, nil,
		),
		startTime: prometheus.NewDesc(
			"process_start_time_seconds", "Start time of the process since unix epoch in seconds.",
			[]string{"process_name", "pid"}, nil,
		),
	}
}

// StartCacheUpdater 启动后台协程，定时刷新进程列表
// interval: 全量扫描的间隔，建议 30s - 60s
func (c *ProcessCollector) StartCacheUpdater(ctx context.Context, interval time.Duration) {
	// 立即执行一次初始化
	c.refreshProcessCache()

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.refreshProcessCache()
			}
		}
	}()
}

// refreshProcessCache 执行全量扫描并更新缓存
// 这是最耗资源的操作，现在只在后台低频执行
func (c *ProcessCollector) refreshProcessCache() {
	// log.Println("Refreshing process cache...") // 调试用

	allProcs, err := process.Processes()
	if err != nil {
		log.Printf("Error scanning processes: %v", err)
		return
	}

	newCache := make(map[int32]CachedProcess)

	for _, p := range allProcs {
		// 获取名称可能会失败（权限或进程刚退出），忽略错误
		name, err := p.Name()
		if err != nil {
			continue
		}

		if c.isTarget(name) {
			newCache[p.Pid] = CachedProcess{
				Proc: p,
				Name: name,
			}
		}
	}

	// 只有在构建完新的 map 后才加锁替换，极大减少锁竞争时间
	c.rwMutex.Lock()
	c.cachedProcs = newCache
	c.rwMutex.Unlock()

	// log.Printf("Cache refreshed. Monitoring %d processes.", len(newCache))
}

func (c *ProcessCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.up
	ch <- c.cpuUser
	ch <- c.cpuSystem
	ch <- c.memoryRSS
	ch <- c.memoryVMS
	ch <- c.numThreads
	ch <- c.openFDs
	ch <- c.startTime
}

func (c *ProcessCollector) Collect(ch chan<- prometheus.Metric) {
	// 1. 获取读锁，复制一份需要采集的列表
	// 我们不想在持有锁的时候进行网络/IO调用（Collect metrics）
	c.rwMutex.RLock()
	// 预分配 slice 提升性能
	targets := make([]CachedProcess, 0, len(c.cachedProcs))
	for _, cached := range c.cachedProcs {
		targets = append(targets, cached)
	}
	c.rwMutex.RUnlock()

	// 2. 遍历列表进行采集
	for _, target := range targets {
		p := target.Proc
		name := target.Name
		pidStr := strconv.Itoa(int(p.Pid))

		// 检查进程是否还存活 (kill signal 0)
		// 这一步是可选的，因为后续的方法如果不存活会报错
		// exists, _ := process.PidExists(p.Pid)

		// 采集 CPU
		times, err := p.Times()
		if err != nil {
			// 如果报错，说明进程可能在两次缓存刷新之间退出了
			// 这里我们选择忽略，等待下一次缓存刷新将其移除
			continue
		}
		ch <- prometheus.MustNewConstMetric(c.cpuUser, prometheus.CounterValue, times.User, name, pidStr)
		ch <- prometheus.MustNewConstMetric(c.cpuSystem, prometheus.CounterValue, times.System, name, pidStr)

		// 采集内存
		mem, err := p.MemoryInfo()
		if err == nil {
			ch <- prometheus.MustNewConstMetric(c.memoryRSS, prometheus.GaugeValue, float64(mem.RSS), name, pidStr)
			ch <- prometheus.MustNewConstMetric(c.memoryVMS, prometheus.GaugeValue, float64(mem.VMS), name, pidStr)
		}

		// 采集线程
		if numThreads, err := p.NumThreads(); err == nil {
			ch <- prometheus.MustNewConstMetric(c.numThreads, prometheus.GaugeValue, float64(numThreads), name, pidStr)
		}

		// 采集句柄
		if fds, err := p.NumFDs(); err == nil {
			ch <- prometheus.MustNewConstMetric(c.openFDs, prometheus.GaugeValue, float64(fds), name, pidStr)
		}

		// 启动时间
		if createTime, err := p.CreateTime(); err == nil {
			ch <- prometheus.MustNewConstMetric(c.startTime, prometheus.GaugeValue, float64(createTime)/1000.0, name, pidStr)
		}

		// UP 指标
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, 1, name, pidStr)
	}
}

func (c *ProcessCollector) isTarget(procName string) bool {
	for _, target := range c.targetNames {
		if strings.Contains(procName, target) {
			return true
		}
	}
	return false
}

func main() {
	addr := flag.String("addr", ":9002", "The address to listen on for HTTP requests.")
	procNames := flag.String("names", "", "Comma separated list of process names to monitor.")
	refreshInterval := flag.Duration("refresh-interval", 30*time.Second, "Interval to refresh process list (scan all processes).")
	flag.Parse()

	if *procNames == "" {
		log.Fatal("Please provide -names (e.g., -names=nginx,mysql)")
	}

	targetList := strings.Split(*procNames, ",")
	collector := NewProcessCollector(targetList)

	// 启动后台刷新协程
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	collector.StartCacheUpdater(ctx, *refreshInterval)

	// ------------------- 修改开始 -------------------

	// 1. 创建一个自定义的注册表 (Registry)
	// 这样就不会包含默认的 Go Runtime 指标和 Exporter 自身的 Process 指标
	r := prometheus.NewRegistry()

	// 2. 将你的采集器注册到这个自定义注册表中
	// MustRegister 如果遇到错误会 Panic，但在新注册表中是安全的
	r.MustRegister(collector)

	// 3. 使用 promhttp.HandlerFor 创建一个专门针对该注册表的 Handler
	handler := promhttp.HandlerFor(r, promhttp.HandlerOpts{
		ErrorLog:      log.Default(),
		ErrorHandling: promhttp.ContinueOnError,
	})

	// 4. 绑定到 HTTP 路由
	http.Handle("/metrics", handler)

	// ------------------- 修改结束 -------------------

	log.Printf("Starting Optimized Process Exporter on %s", *addr)
	log.Printf("Monitoring: %v", targetList)
	log.Printf("Process list refresh interval: %v", *refreshInterval)

	if err := http.ListenAndServe(*addr, nil); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
