.PHONY: run build test infra-up infra-down clean

# 运行项目
run:
	go run cmd/main.go

# 编译
build:
	go build -o bin/student-coach cmd/main.go

# 运行测试
test:
	go test ./... -v

# 启动基础设施（Milvus + Redis + MySQL）
infra-up:
	docker-compose up -d

# 停止基础设施
infra-down:
	docker-compose down

# 查看基础设施状态
infra-status:
	docker-compose ps

# 清理编译产物
clean:
	rm -rf bin/
