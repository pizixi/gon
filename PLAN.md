# Gon 实现方案与进度

纯 Go（Gio）原生 Windows 终端，无 webview、无 cgo。磨砂 / 半透明 / 壁纸 / 左侧多标签 / 平铺分屏。

## 1. 分层

| 包 | 职责 |
| --- | --- |
| `internal/config` | JSON 配置：shells、字体、窗口、主题、壁纸、回滚行数、光标闪烁 |
| `internal/term` | 屏幕缓冲、VT/ANSI 解析、调色板、按键与鼠标编码、粘贴 |
| `internal/pty` | ConPTY 会话（Win32 直调，无 cgo），异步写入队列、退出检测 |
| `internal/fontset` | 系统字体索引、样式变体、CJK 回退 |
| `internal/render` | 字形图集（彩色预乘 sprite）、单纹理渲染器、文本测量 |
| `internal/ui` | 窗口、主题、标题栏、侧栏标签、平铺分屏、设置、壁纸 |

## 2. 渲染管线

1. 字形首次使用时用 `x/image/vector` 光栅化成 8 位覆盖率位图，裁边后按
   `{face, gid, ppem, bold, rgb, alpha}` 缓存。
2. 预乘 alpha 写入 1024×1024 图集；`generation` 递增。
3. 渲染器每帧检查 generation，有变化才 `paint.NewImageOp` 重建纹理（每帧最多一次）。
4. 绘制单个字形：
   ```
   offset(dx-u0, dy-v0)  →  clip(atlas 字形矩形)  →  ImageOp  →  PaintOp
   ```
5. 单元背景先批量填充（同色合并为游程），再画字形与下划线/删除线，最后画光标
   （反色 + 主题色 + 闪烁）。
6. UI 文本复用同一图集（`render.Text`），界面与终端共享一个纹理。

## 3. 输入

- 文本：`key.FocusFilter`（Windows 的 WM_CHAR 经 Gio 转为 `key.EditEvent`），
  因此 IME / 中日韩输入可直接送入 shell。
- 控制键：`key.Filter{Focus: paneTag}` → `term.EncodeKey` → PTY。
- 应用快捷键在送入 shell 之前处理：
  - `Ctrl+Shift+T` 新标签、`Ctrl+Shift+W` 关窗格、`Ctrl+Tab` / `Ctrl+Shift+Tab` 切换
  - `Alt+Shift++` / `Alt+Shift+-` 分屏（左右 / 上下），`Alt+Shift+W` 关窗格
  - `Alt+方向` 窗格聚焦，`Ctrl+=`/`Ctrl+-`/`Ctrl+0` 缩放，`F11` 全屏
  - `Ctrl+Shift+C` 复制；`Ctrl+C` 有选区时复制、否则发送 `^C`；`Ctrl+V` / `Ctrl+Shift+V` 粘贴
- 鼠标：应用请求鼠标模式（1000/1002/1003 + SGR 1006）时转发；否则本地拖选、
  滚轮回看、右键粘贴、点击切换焦点。
- 滚轮：按单元格高度换算成行；仅在回滚视图中滚动，输入时自动回到末尾。

## 4. 窗口与外观

- `app.Decorated(false)`，自绘标题栏；标题栏区域注册
  `system.ActionInputOp(system.ActionMove)`，由 Gio 的 WM_NCHITTEST 转成 `HTCAPTION`；
  边框缩放同样由 Gio 处理。
- Win32 原生移窗期间复用已提交的画面，结束移动后刷新。边框缩放和 DPI 变化按新尺寸绘制。
- 窗口按钮用矢量路径绘制（不依赖字体是否有 ✕ / ▢ 字形）。
- 半透明：终端背景 alpha 来自 `window.opacity`，面板叠加半透明色。
- 壁纸：解码后降采样 + 两次盒式模糊（半径来自 `window.blur`），cover 方式铺满。
- DWM 配置暗色标题和窗口圆角，磨砂在应用内绘制。

## 5. 分屏模型

- 每个标签维护一棵分屏树，跨标签分屏也使用分屏树。添加窗格或标签时，根据全局空间和各分支的最小尺寸分配区域。
- 每条分隔线保存切分比例，窗口缩放时重新计算两侧的像素边界和终端行列数。
- 拖动标题栏或底部栏可交换同一标签的窗格位置。将底部栏拖到另一个标签可交换两个标签的位置。分隔线与底部把手共用拖动调整逻辑。关闭窗格时，同一分支的剩余窗格接管空间。
- 焦点切换同时触发键盘焦点转移（`key.FocusCmd`）。

## 6. 线程模型

- UI 线程：Gio 事件循环，所有布局与绘制。
- 每个窗格一个读 goroutine：读 ConPTY → 解析（持窗格锁）→ `Invalidate`。
- 每个窗格一个写 goroutine：缓冲队列 → PTY，避免 UI 被管道写阻塞。
- 退出：`WaitForSingleObject` 的 goroutine 关闭会话 `done`，窗格标记已退出。
- 关闭顺序：`CancelIoEx` → `ClosePseudoConsole` → 关闭句柄（幂等；HPCON 只关一次）。

## 7. 交付物

- [x] `cmd/gon`：入口，加载配置、创建无边框窗口
- [x] 配置读写与默认值、示例配置
- [x] VT 解析与编码测试、ConPTY 端到端测试
- [x] 字形光栅化、无头 GPU 渲染像素测试
- [x] 分屏几何、标签焦点、配置校验测试
- [x] README（构建、配置、快捷键）与界面截图

## 8. 后续可扩展

- 命令面板 / 快捷搜索、标签重命名与拖拽排序
- 选择即复制、双击选词、带拖动的滚动条
- 会话持久化、与 Windows Terminal 配置互通
