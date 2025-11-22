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
