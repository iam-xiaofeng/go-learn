# 消息队列与数据库高并发深度剖析

---

## 一、RabbitMQ 断电恢复机制：持久化铁三角

当机器突然遭遇断电、被强制 `kill -9` 时，RabbitMQ 里的消息能否完好无损地复原？

**答案是：能！但必须严格集齐“持久化铁三角”！**

```text
       ┌── 1. 队列持久化: durable: true (保证重启后“盘子”还在)
铁三角 ┼── 2. 消息持久化: DeliveryMode: amqp.Persistent (保证盘子里的“菜”写入磁盘)
       └── 3. 手动确认机制: autoAck: false (保证消费者吃到一半暴毙时，菜能吐回盘子重试)
```

### 1. 核心代码规范
```go
// 1. 队列持久化：元数据落盘
_, err = channel.QueueDeclare("tasks", true /* durable */, false, false, false, nil)

// 2. 消息持久化：消息内容写入 Message Store 磁盘段文件
_ = channel.PublishWithContext(ctx, "", "tasks", false, false, amqp.Publishing{
    DeliveryMode: amqp.Persistent, // 标记为 2
    ContentType:  "application/json",
    Body:         body,
})

// 3. 手动 ACK：消费者完成业务后显式通知
msgs, _ := channel.Consume("tasks", "", false /* autoAck = false */, false, false, false, nil)
for d := range msgs {
    process(d.Body)
    _ = d.Ack(false) // 告诉 RabbitMQ：我处理完了，现在你可以从磁盘安全抹除了！
}
```

### 2. 为什么半路崩溃不会丢消息？
若 Worker 从队列拉取了 Task 正在调用大模型，此时 Worker 机器发生断电崩溃：
因为 RabbitMQ **从未收到该 Worker 的 `d.Ack(false)`**，RabbitMQ 检测到 TCP 连接断开后，会**自动将这条消息重新放回队列头部（Redelivery）**，重新下发给健康的 Worker 处理。

---

## 二、RabbitMQ（传统 MQ）vs Kafka（事件流）的本质差异

| 核心维度 | RabbitMQ (AMQP 规范) | Apache Kafka (分布式流) |
| :--- | :--- | :--- |
| **消息命运** | **“用完即弃 / 阅后即焚（Delete on Ack）”** | **“大账本永久追加（Commit Log Retention）”** |
| **存储形态** | 临时流动管道，消息消费确认后**物理擦除** | 顺序写入磁盘文件，保留 7 天、30 天甚至永久 |
| **消费模型** | Push 推模型为主（Broker 主动推给 Worker） | Pull 拉模型（Consumer 自主维护消费游标 Offset）|
| **重放能力** | 默认不支持重放历史消息 | 支持将 Offset 拨回过去任意时间点**完整重放历史** |
| **典型场景** | Agent 异步任务分发、长任务调度、削峰填谷 | 海量大数据埋点、用户点击流、日志汇聚、实时数仓 |

---

## 三、MongoDB 并发控制与锁机制剖析

许多开发者误以为 MongoDB 只能单线程写或有大表锁，这早已过时。

### 1. 存储引擎 WiredTiger 内部机制
- **并发写操作（Insert / Update）**：
  - 采用 **文档级锁（Document-Level Locking）**；
  - 两个 Goroutine 分别插入不同 `_id` 的消息时，**互不阻塞，各自写入自己的文档**。
  - **性能瓶颈在哪？** 瓶颈在 **B-Tree 索引维护**（高并发插入同一张表时索引树需要短暂旋转重平衡）以及 **Journal 预写日志（WAL）的磁盘刷盘排队**。
- **并发读操作（Find / FindOne）**：
  - 采用 **MVCC（多版本并发控制）+ 快照隔离（Snapshot Isolation）**；
  - 100 个并发读同一个 Session 时，**完全零锁争抢（Lock-free）**，直接读内存快照版本。

---

## 四、RabbitMQ Go 驱动单信道互斥锁（Single-Channel Lock）陷阱

在 Go 官方驱动 `amqp091-go` 中，**绝对不能让所有并发 Goroutine 共用一个 `*amqp.Channel`**！

### 驱动底层源码真相：
```go
func (ch *Channel) send(m message) error {
    ch.m.Lock()         // ⚠️ 单信道互斥锁！
    defer ch.m.Unlock()
    return ch.connection.send(m)
}
```
如果 100 个 Goroutine 共用同一个 Channel 调用 `PublishWithContext`，**所有并发协程都会强制降级为在 `ch.m.Lock()` 面前排长队**。
- **正解**：为每个并发 Worker 分配专属的独立 `*amqp.Channel`（分配独立 Channel ID，如 1~10），真正实现网卡上的并行传输。
