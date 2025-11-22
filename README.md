# 监控进程资源情况

基于 Prometheus + Grafana 监控进程资源情况

## self-process-exporter

需要采集的常见指标：

- CPU 使用时间（User/System）
- 内存使用量（RSS/VMS）
- 文件句柄数
- 线程数
- 进程启动时间
- 进程状态

```bash
# 本地启动试试
go run ./self-process-exporter -addr :9002 -names nginx

# 编译
GO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ./self-process-exporter ./self-process-exporter
sudo chmod +x ./self-process-exporter/self-process-exporter

# 本地启动试试
sudo ./self-process-exporter/self-process-exporter -addr :9002 -names nginx

# 发到服务器
sudo scp ./self-process-exporter/self-process-exporter manager@192.168.8.58:/home/manager/self-process-exporter
sudo cp ./self-process-exporter /usr/local/bin/self-process-exporter

# 疯狂请求 nginx
while true; do curl -s "http://127.0.0.1:80/" > /dev/null; done
```

## 服务

```bash
sudo vim pme.service
sudo chmod +x ./pme.service
sudo cp ./pme.service /lib/systemd/system/pme.service
sudo vim /lib/systemd/system/pme.service
systemctl daemon-reload
sudo systemctl status pme
sudo systemctl restart pme
```

##  Grafana Dashboard JSON 文件

使用方法

1. 将下面的 JSON 代码复制并保存为 process-dashboard.json。
2. 在 Grafana 中点击左侧 Dashboards -> New -> Import。
3. 上传该文件或将内容粘贴到文本框中。

重要：在 Import 界面，选择你的 Prometheus 数据源。
