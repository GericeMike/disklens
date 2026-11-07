package scanner

import "strings"

// categoryOf 按扩展名归类文件类型（中文大类）
func categoryOf(ext string) string {
	if ext == "" { return "无扩展名" }
	if cat, ok := extMap[ext]; ok { return cat }
	return "其他"
}

var extMap = buildExtMap()

// CategoryOf 按扩展名返回中文大类（导出供 GUI 类型明细列表过滤用）
func CategoryOf(ext string) string { return categoryOf(ext) }

// CatOrder 类型直方图固定桶序（Node.Cat/CatF 与之一一对应；
// 增量嫁接按桶搬运统计，桶序永不改动，新增大类只许追加）
var CatOrder = []string{"视频", "音频", "图片", "文档", "压缩包", "开发与代码", "虚拟机与镜像", "数据库", "日志", "无扩展名", "其他"}

var catIdx = buildCatIdx()

func buildCatIdx() map[string]int {
	m := make(map[string]int, len(CatOrder))
	for i, c := range CatOrder {
		m[c] = i
	}
	return m
}

// catIndexOf 大类 → 直方图桶下标（未知大类归入「其他」）
func catIndexOf(cat string) int {
	if i, ok := catIdx[cat]; ok {
		return i
	}
	return catIdx["其他"]
}

var catCount = len(CatOrder)

func buildExtMap() map[string]string {
	m := map[string]string{}
	add := func(cat string, exts ...string) {
		for _, e := range exts { m[e] = cat }
	}
	add("视频", ".mp4", ".mkv", ".avi", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".ts", ".rmvb", ".3gp")
	add("音频", ".mp3", ".flac", ".wav", ".aac", ".ogg", ".m4a", ".wma", ".ape")
	add("图片", ".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".heic", ".raw", ".cr2", ".nef", ".svg", ".ico", ".tif", ".tiff", ".psd")
	add("文档", ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".txt", ".md", ".csv", ".epub", ".mobi", ".odt", ".wps", ".et")
	add("压缩包", ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz", ".iso", ".cab", ".msi", ".tgz")
	add("开发与代码", ".go", ".js", ".ts", ".py", ".java", ".c", ".cpp", ".h", ".rs", ".cs", ".html", ".css", ".json", ".xml", ".yml", ".yaml", ".sh", ".bat", ".sql", ".php", ".rb", ".vue", ".jsx", ".tsx", ".dll", ".pdb", ".lib", ".exe.mui")
	add("虚拟机与镜像", ".vhdx", ".vhd", ".vmdk", ".qcow2", ".vdi", ".img", ".wim", ".dmg")
	add("数据库", ".db", ".sqlite", ".sqlite3", ".mdb")
	add("日志", ".log")
	return m
}

var _ = strings.ToLower
