package services

import (
	"context"
	"hybrid-infra/go/host"
)

// DocumentService 暴露文档交换与打印入口，具体权限、文件归属和大小限制由宿主独立检查。
type DocumentService struct{ manager *host.Documents }

// OpenPrintPreview 用纯文本表格生成本地报表并在系统默认浏览器打开，由用户选择打印或另存 PDF。
func (s *DocumentService) OpenPrintPreview(ctx context.Context, report host.BusinessReport) error {
	return s.manager.OpenPrintPreview(ctx, report)
}

// Parse 解析 CSV、JSON、XLSX 或表格 ZIP 的 Base64 内容，只预览，不自动入库。
func (s *DocumentService) Parse(ctx context.Context, format string, data []byte) (host.BusinessTable, error) {
	return s.manager.Parse(ctx, format, data)
}

// ImportFile 使用当前窗口的文件凭证读取并校验表格。
func (s *DocumentService) ImportFile(ctx context.Context, token, format string) (host.BusinessTable, error) {
	return s.manager.ImportFile(ctx, token, format)
}

// ExportFile 将表格导出到用户选择的可写凭证，成功后返回实际字节数。
func (s *DocumentService) ExportFile(ctx context.Context, token, format string, table host.BusinessTable) (host.DocumentExport, error) {
	return s.manager.ExportFile(ctx, token, format, table)
}

// Print 请求当前窗口显示系统打印预览，完成状态由系统对话框掌握，不能作为已打印成功的回执。
func (s *DocumentService) Print(ctx context.Context) error { return s.manager.Print(ctx) }
