# 架构与维护约定

## 启动与调用链

```text
settings.json / 环境变量
        ↓
main.go → Wails App → 主窗口（内置资源或远程 URL）
        ↓                 ↓
services.Register     JS bindings / runtime
        ↑                 ↓
        └────── Go 服务调用 ┘
                   ↓
          窗口、弹框、KV、日志服务
```

- main.go：配置校验、资产中间件、主窗口、服务注册、随应用上下文停止的时间事件。
- go/setting：配置查找、验证、稳定的数据目录。无配置可用默认值；显式指定错误文件或无效内容时启动失败。
- go/services/ui.go：弹框内容序列化、Confirm 的请求上下文与事件生命周期。
- go/utils/cw.go：集中创建窗口、尺寸与位置回退、全屏状态。
- go/utils/cross：直接使用 Wails ScreenManager，避免维护错误的 Win32/Cocoa/X11 重复实现。
- frontend：用于本地演示与内置弹框资源，不承担业务框架约束。
- scripts/generate-hybrid.mjs：生成并改写独立前端包，使用 Node 文件 API，移除对 Bash/sed/eval 的依赖。
- scripts/hybrid-client.js：生成包 facade 的维护源；修改后重新导出。

## Confirm 生命周期

使用 atomic.Add 生成独立事件名，同时作为窗口名；在 Show 前订阅结果及关闭事件。仅接受来源窗口名匹配、值为字符串 "0" 或 "1" 的结果。sync.Once 保证重复回包只交付一次。

任意一个条件结束等待：正常回包、窗口关闭、请求取消、应用关闭、超时。返回时释放结果监听、窗口监听、超时上下文和窗口。事件回调只交付结果，关闭窗口统一在调用退出时执行。

JSON 序列化后的内容作为 JS 对象传入；DOM 未就绪时等待 DOMContentLoaded。普通弹框使用 textContent，Show 单独使用 innerHTML。新增服务不可恢复直接拼接 JS 字符串的模式。

## 配置与兼容性

用户配置优先于随程序部署的配置，开发工作目录只在含 go.mod 时参与查找。用户 KV 文件放在固定的应用数据目录，初次使用创建目录。历史 store.json 迁移由操作者显式复制，防止误覆盖已有用户数据。

前端服务签名变更后应重新生成两组 bindings（内置 frontend/bindings 和外部 hybrid/bindings），并运行接入包测试。三个 Wails 版本必须一致；不要在 package.json 中使用 latest。

F12 由主窗口配置 debug 控制，子窗口使用各自的 f12DevTools 选项。debug 不等价于系统级禁用开发者工具。

## 发布检查

1. 核对 Go、CLI、runtime 版本；安装依赖并生成 frontend/dist。
2. 运行 Go 测试、go vet 和 Node 接入测试。
3. 更新内置 bindings 与 hybrid 导出，检查 manifest 和差异。
4. 构建目标平台的桌面可执行文件；跨平台的 server 编译仅检查公共代码，不能替代原生桌面编译。
5. 按升级文档的人工验收清单在目标 WebView 上检查弹框、窗口与外部接入。

Android/iOS 构建文件延续仓库原有模板，本次没有完成移动端迁移或验收；不能把目录存在当作支持承诺。
