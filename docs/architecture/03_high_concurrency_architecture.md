# 高并发架构演进与两级生产者-消费者模式

---

## 一、Pprof 火焰图与阿姆达尔定律（Amdahl's Law）的实战教训

在真实的基准压测中，我们抓取了 CPU 火焰图：
- **初始瓶颈分布**：
  - MongoDB `SaveMessage`（写磁盘落盘）：**占 ~45%**
  - MongoDB `GetSession`（每次发消息前额外查一次）：**占 ~25%**
  - RabbitMQ `PublishWithContext`（单信道锁 + 磁盘落盘）：**占 ~20%**
  - Go / Gin 框架本身：**占不到 10%**

### 优化迷思：为什么加了 Redis 缓存，QPS 依然停留在 ~2600-2700？
我们引入 Redis 拦截了 `GetSession`（25% 耗时），本以为 QPS 会翻倍，结果 QPS 依然在 2600~2700 波动。

**数学根源：阿姆达尔定律（Amdahl's Law）**
单次 HTTP 请求的耗时账单：
- 读 Redis：~1ms
- 写 MongoDB 磁盘：~17ms
- 写 RabbitMQ 磁盘：~17ms
- **总耗时**：$1\text{ms} + 17\text{ms} + 17\text{ms} = 35\text{ms}$。
- 引入 Redis 仅从 37ms 缩短到 35ms，只优化了 5% 的边缘耗时，**两个各占 17ms 的磁盘落盘巨无霸完全未被触及**！
- 同时，Mac 上 Docker 虚拟机（Hypervisor + VirtioFS）的物理磁盘随机 IOPS 极限大约就在每秒 5,000 次左右，被双写磁盘彻底锁死。

---

## 二、破解“内存丢数据 vs 磁盘太慢”的无限死循环

分布式系统经典悖论：
> *“为了快放内存，断电就丢；为了不丢放磁盘，磁盘又慢；如果把磁盘操作再放内存，岂不陷入死循环？”*

### 工业界终极破局之道：数据等级划分（Data Tiering）

```text
               ┌── 等级 1: 核心事实数据 (Core Fact) ──► 绝不能丢！同步落库 (MongoDB messages)
数据命运两分法 ─┤
               └── 等级 2: 派生衍生任务 (Derived Task) ─► 享受内存高并发，靠核心数据做断电补偿！
```

1. **核心数据（用户发了什么话）**：
   - 同步写 Mongo，强一致性，断电不丢。
2. **边缘任务（提取记忆、算 Token、Embedding）**：
   - 原始素材已经在 Mongo 里了。即使在内存队列里断电丢失，**重启后只需一个补偿任务扫一眼 Mongo：`where memory_extracted == false`，重新重放即可，零数据丢失！**
3. **消费者完成闭环**：
   - 消费者抽取记忆完成后，将结果存入 `memories` 集合，并在 `messages` 表中打上 `status: "processed"` 标记，实现**幂等防重（防止网络闪烁重复计费）**。

---

## 三、两级生产者-消费者架构（Two-Stage SEDA）

为了打破单信道互斥锁与 HTTP 线程阻塞，大厂标准的终极架构为**两级解耦**：

```text
[HTTP 请求]
    │
    ▼
【第一级：进程内内存级 (微秒级响应)】
  • 生产者：HTTP Handler 接待员
  • 队  列：Go 原生 buffered chan (容量 50,000)
  • 行为：非阻塞或 0.0001ms 快速投递，立即返回前端 201 Created！
    │
    ▼
【第二级：分布式集群级 (跨机器/高可靠级)】
  • 生产者：专职 Goroutine 任务池 (例如 10 个独立 Worker)
  • 关键设计：【每个 Worker 独享专属的一个 *amqp.Channel！】
  • 队  列：RabbitMQ 集群
  • 消费者：专职消费者 Goroutine 池 (各自独享独立 *amqp.Channel + QoS 预取)
```

---

## 四、历史基线性能记录（重构前 Baseline）

在 Mac 单机运行完整全链路（HTTP + MongoDB 真实落盘 + Redis 缓存 + RabbitMQ 持久化消费）的实测数据：

- **压测参数**：`-c 100`（100 并发协程），`-n 25000`（总请求 25,000 次）
- **重构前基线（Baseline）**：
  - 轮次 1：`2503.37 req/sec`（Avg: 39.88ms, P50: 32.25ms, P99: 198.73ms）
  - 轮次 2：`2787.86 req/sec`（Avg: 35.81ms, P50: 27.21ms, P99: 153.88ms）
  - 轮次 3：`2629.88 req/sec`（Avg: 37.94ms, P50: 29.23ms, P99: 151.46ms）
  - **平均基线吞吐量**：**~2650 QPS**，**平均延迟：~37ms**。
