package host

import "hybrid-infra/go/utils"

const DeveloperDocsPage = "/shell/docs.html"
const DeveloperDocsWindow = "hybrid-infra-docs"

// OpenDeveloperDocs 打开或聚焦固定的开发文档与示例窗口，使用内置页面能力上限；实际调用仍受配置中的来源授权约束。
func (c *Controller) OpenDeveloperDocs() (WindowInfo, error) {
	return c.open(utils.NewWindowOptions{Name: DeveloperDocsWindow, Title: "开发文档 · Hybrid", Url: DeveloperDocsPage, Width: 1220, Height: 860, F5Reload: true, F12DevTools: true}, Capabilities, DeveloperDocsWindow)
}
