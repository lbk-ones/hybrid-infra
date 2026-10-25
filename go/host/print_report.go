package host

import (
	"bytes"
	"html/template"
	"strings"
)

// BusinessReport 包含纯文本表格与有界的报表说明，纸张和方向采用固定枚举，不能注入任意 HTML 或 CSS。
type BusinessReport struct {
	BusinessTable
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Footer      string `json:"footer"`
	Paper       string `json:"paper"`
	Orientation string `json:"orientation"`
}

var printReportTemplate = template.Must(template.New("report").Parse(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><title>{{.Title}} · 打印预览</title><style>
@page{size:{{.Paper}} {{.Orientation}};margin:12mm}*{box-sizing:border-box}body{font:14px/1.6 'Segoe UI','Microsoft YaHei',sans-serif;color:#243b2e;background:#f3f7f4;margin:0}.controls{background:white;border-bottom:1px solid #dce5df;padding:16px 24px}button{font:inherit;border:0;border-radius:6px;background:#21765e;color:white;padding:9px 18px;cursor:pointer}.controls p{font-size:12px;color:#667a6d}article{background:white;padding:28px;max-width:1100px;margin:24px auto;border:1px solid #dce5df}h1{font-size:24px;margin:0}p{white-space:pre-wrap}table{width:100%;border-collapse:collapse;table-layout:fixed}th,td{border:1px solid #cbd8cf;padding:8px;text-align:left;overflow-wrap:anywhere;white-space:pre-wrap}th{background:#edf4ef}thead{display:table-header-group}tr{break-inside:avoid}@media print{body{background:white}.controls{display:none}article{max-width:none;border:0;margin:0;padding:0}*{print-color-adjust:exact;-webkit-print-color-adjust:exact}}</style></head><body><div class="controls"><button onclick="window.print()">打印 / 另存为 PDF</button><p>在系统打印预览中选择打印机、纸张、方向，或另存为 PDF。此文件是临时报表，请在关闭外壳前完成保存。</p></div><article><h1>{{.Title}}</h1><p>{{.Subtitle}}</p><table><thead><tr>{{range .Headers}}<th>{{.}}</th>{{end}}</tr></thead><tbody>{{range .Rows}}<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>{{end}}</tbody></table><p>{{.Footer}}</p></article></body></html>`))

// renderPrintReport 校验字段后使用自动转义模板生成本地报表，不引用外部网络资源、不执行业务提供的脚本。
func renderPrintReport(report BusinessReport) ([]byte, error) {
	report.Warnings = []string{}
	if err := validateTable(report.BusinessTable); err != nil {
		return nil, err
	}
	if len([]rune(report.Title)) > 120 || len(report.Subtitle) > 2048 || len(report.Footer) > 2048 {
		return nil, Fail("E_INVALID_ARGUMENT", "报表标题最多 120 字，说明和页尾最多 2048 字节")
	}
	if strings.TrimSpace(report.Title) == "" {
		report.Title = "业务报表"
	}
	if report.Paper == "" {
		report.Paper = "A4"
	}
	if report.Orientation == "" {
		report.Orientation = "portrait"
	}
	if report.Paper != "A4" && report.Paper != "Letter" || report.Orientation != "portrait" && report.Orientation != "landscape" {
		return nil, Fail("E_INVALID_ARGUMENT", "纸张仅支持 A4/Letter，方向为 portrait/landscape")
	}
	var output bytes.Buffer
	if err := printReportTemplate.Execute(&output, report); err != nil {
		return nil, Fail("E_INVALID_TABLE", "报表生成失败")
	}
	return output.Bytes(), nil
}
