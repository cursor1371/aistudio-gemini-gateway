package server

import _ "embed"

// dashboardHTML 是嵌入到二进制中的可视化状态面板页面。
// 通过 go:embed 内嵌，避免单独部署静态资源，最大程度减少侵入。
//
//go:embed dashboard.html
var dashboardHTML string
