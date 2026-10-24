package services

type GreetService struct{}

// Greet 返回包含传入姓名的问候文本，作为前端调用 Go 服务的最小示例。
func (g *GreetService) Greet(name string) string {
	return "Hello " + name + "!"
}
