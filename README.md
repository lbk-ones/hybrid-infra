# install task cli
```shell
 pnpm install -g @go-task/cli
```
doc: https://taskfile.dev/

# dev
需要安装pnpm
```shell
 task dev
```

# build 
```shell
 task build
```

# cross
```shell
# 生成image
 task setup:docker
```


# generate hybrid
生成hybrid引入：如果没有改动，尽量不要执行这个命令
```shell
task hybrid
```
- 目前只生成js版本
- 要想使用hybrid能力则需要将生成的hybrid文件夹整个搬走到其他需要使用的前端项目中去然后调用即可