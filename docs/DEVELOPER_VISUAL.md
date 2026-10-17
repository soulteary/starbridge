# UIA 与视觉操作流程

`ui.observe` 同时提供窗口图像与可选 UIA 查询。优先使用 UIA 的真实 selector 和 `ui.act`；应用没有可用控件树时，MCP 客户端中的视觉模型或 OCR 可以在图片上提出一个候选位置，再交给 `ui.click`。桥接层不调用模型、不保存模型凭据，也不把图片坐标伪装成控件 ID。

安装完整的 `developer/` 目录，包括 `visual-flow.ps1` 和 workers。两动作都要求目标所在的 Windows 交互会话，窗口身份必须包含 `hwnd`、`pid`、`start_time_utc`。

## 观察与点击

先准备 `window.json`，填入实际窗口身份：

```json
{"hwnd":"0x123","pid":42,"start_time_utc":"2026-01-02T03:04:05Z"}
```

```sh
starbridge dev ui.observe VM --request window.json
```

`data.screenshot` 沿用 [截图契约](DEVELOPER_SCREENSHOTS.md)，包含有界 PNG、实际捕获边界、时间、尺寸和哈希。`data.uia.status` 为 `available`、`unavailable` 或 `not_requested`；可用时携带 `data` 和真实 `complete`，失败时仅给稳定 `error_code`。UIA 不支持、超时或扫描不完整不会被解释成“没有控件”。图像与 UIA 是相邻的独立观察，各自保留时间，不承诺原子快照。

`data.observation` 是可原样传回的元数据，包含 Windows 当前用户作用域的 DPAPI token。token 不写入 guest 文件，硬有效期 30 秒，并绑定窗口身份、会话、DPI、图片哈希、尺寸、实际捕获 `rect` 和独立的 Win32 `window_rect`。它防止误改元数据，不是针对能控制同一 Windows 用户的代码的安全边界。

调用 `ui.click` 时，`observation` 使用该对象，`target` 提供一个点或一个矩形；矩形取中心，不接受候选数组：

```json
{"target":{"x":120,"y":40,"width":60,"height":24,"image_width":800,"image_height":600},"focus":true,"max_age_ms":15000}
```

上例只展示候选字段，实际请求还必须包含本次生成的 `observation`。坐标从图片左上角开始，单位为候选图片像素；未指定 `image_width/image_height` 时使用原图尺寸。允许完整图片缩放，按像素中心映射到实际捕获边界；不能把裁剪、加边框或拼接图片的坐标当作原图坐标。负坐标显示器受支持，不额外乘 DPI 缩放系数。

可运行的 [Python 客户端示例](../examples/visual-flow.py) 负责生成请求、保存 PNG 和传回 token，不包含 OCR 或模型接口：

```sh
python3 examples/visual-flow.py observe VM --hwnd 0x123 --pid 42 \
  --start-time-utc 2026-01-02T03:04:05Z --output /tmp/visual-before
# 在有效期内，根据 before/screenshot.png 给出候选；需要激活窗口才加 --focus。
python3 examples/visual-flow.py click VM --observation /tmp/visual-before/result.json \
  --x 120 --y 40 --focus --output /tmp/visual-after
```

示例中的窗口身份和候选需要替换成当前目标。`--output` 必须是新目录；示例在输入前独占创建 0700 目录与 0600 结果文件，拒绝覆盖旧观察。CLI 点击显式传入 `--allow-write`；执行后保存新截图与结果。token 过期后重新观察和定位，不能修改时间戳延长有效期。

## 校验与结果

`ui.click` 在一个 Windows session mutex 内核对 token、年龄、HWND/PID/进程启动时间、会话、Win32 几何和 DPI。只有显式 `focus:true` 才请求普通前台激活；激活后再次校验。最终调用现有 `input.mouse` 核心，继续检查前台窗口、客户区、遮挡窗口以及物理输入状态。拒绝过期、越界、已移动、已缩放或身份改变的观察；不会自动重试点击。

默认只检查几何和新鲜度，不能保证截图内容未变。`strict_image_match:true` 另外按原捕获方法复拍并比对完整 PNG 哈希；动画、光标闪烁、hover 或聚焦造成的变化都可能拒绝。默认允许图像内容变化是可用性取舍，客户端仍需判断候选是否足够明确。

点击成功响应只表示 `outcome:"dispatched"`，`verification.status` 始终为 `pending`。默认等待 250ms 后返回新的 `observation`、PNG 和可选 UIA；`wait_ms` 可设 0–2000，仅是采样延迟。业务结果应通过新图片、`ui.query` 或 [等待/断言](DEVELOPER_WAIT.md) 明确验证，不能把派发当作“保存成功”等业务结论。

`include_uia` 默认 true，固定限制 64 项、500 个扫描节点、深度 6，不读取控件值。`uia_timeout_ms` 默认 1500，范围 1000–5000；`timeout_ms` 默认 15000，范围 2000–20000，UIA 使用视觉 worker 结束后的剩余等待预算。不足 1000ms 时直接降级为 UIA_TIMEOUT。进程启动和传输也受外层 CLI 超时约束。

`max_age_ms` 默认 15000，范围 100–30000；使用 Windows guest 时钟计算，避免宿主时钟偏差。`max_bytes/max_pixels` 沿用截图预算。`capture_method` 委托安装版本的截图能力校验；点击省略时继承观察的捕获方法。基础版本使用默认 `print_window`，不能据此假定应用一定会提供有效画面。

启动前拒绝返回 `error.details.outcome:"not_started"`；聚焦或输入可能开始后失败、worker/传输超时，以及点击后的截图失败均返回 `unknown`。聚焦本身也可能改变窗口状态。先观察实际结果，再决定下一步；桥接层不会撤销输入。

## MCP

开启 `serve --developer-tools` 后可调用 `dev.ui.observe`；点击另需 `--allow-guest-write`，不需要宿主 workspace。成功时图片作为原生 MCP `image/png` 返回，文本和 structured content 仅保留元数据，均不重复 Base64。

策略按 VM UUID 逐一检查嵌套动作：

- `ui.observe`：`dev.ui.observe`、`dev.screenshot.capture`；默认还需 `dev.ui.query`。
- `ui.click`：`dev.ui.click`、`dev.screenshot.capture`、`dev.input.mouse`；默认还需 `dev.ui.query`，`focus:true` 另需 `dev.window.focus`。

设置 `include_uia:false` 时不要求 UIA grant。拒绝授权时不会调用 guest 或枚举 VM。工具保留既有 VM 写协调和 guest session mutex；读取仍可能与其他客户端的写入交错，token 校验不会锁定应用内容。

Go 测试使用生成的 PNG 和 fake transport/MCP SDK。`tests/windows/developer-visual-contract.ps1` 使用合成 native/input 替身和真实 DPAPI，验证映射、过期、篡改、会话/DPI/几何变化、单次派发、新观察与 UIA 降级；不操作真实桌面。真实应用渲染、前台激活与鼠标命中仍需单独桌面验收。
