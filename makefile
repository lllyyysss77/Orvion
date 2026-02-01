mkdb:
	mkdir -p db

tidy:
	go mod tidy

fmt:
	go fmt ./...

# 启动后端以及将前端移动到后端
run:
	go run .

add: fmt tidy
	git add .

.PHONY: webui

# 打包前端
webui: 
	cd webui && npm install && npm run build

# 一键启动
all:
	make webui && make run