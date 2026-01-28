package utils

import "net/http"

// HttpMiddleWare http 中间件
func HttpMiddleWare(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. 设置核心跨域响应头
		// 允许所有源（生产环境建议指定具体域名，如"https://your-frontend.com"）
		w.Header().Set("Access-Control-Allow-Origin", "*")
		// 允许的HTTP方法
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		// 允许的请求头
		w.Header().Set("Access-Control-Allow-Headers", "*")
		// 允许携带Cookie（如果需要，需将Allow-Origin设为具体域名，不能是*）
		// w.Header().Set("Access-Control-Allow-Credentials", "true")
		// 预检请求缓存时间
		w.Header().Set("Access-Control-Max-Age", "86400") // 24小时
		// 2. 处理预检请求（OPTIONS方法）：直接返回200
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		// 3. 调用下一个处理器（业务逻辑）
		next.ServeHTTP(w, r)
	})
}
