# UIA 与视觉操作流程

优先使用[控件查询](DEVELOPER_UI_QUERY.md)提供的真实 selector 和 [ui.act](DEVELOPER_UI_ACTIONS.md)。应用没有可用控件树时，`ui.observe` 提供图像与可选 UIA，客户端的视觉模型、OCR 或使用者提出一个候选位置，`ui.click` 校验观察后点击并返回新图像。桥接层不调用模型或 OCR，不保存模型凭据，也不把图片坐标当作控件 ID。

安装完整的 `developer/` 目录。两动作均需目标所在的 Windows 交互会话；观察需 `hwnd`、`pid`、`start_time_utc`，点击通过前次观察携带同一身份。

## 请求一次观察

保存以下合成参数为 `window.json`。预演不访问 VM；实际执行需替换当前测试窗口身份：

```json
{"hwnd":"0x123","pid":42,"start_time_utc":"2026-01-02T03:04:05Z","include_uia":true,"capture_method":"print_window"}
```

```sh
./bin/starbridge --dry-run dev ui.observe fixture-vm --request window.json
# 替换身份后保存实际响应，供客户端读取：
./bin/starbridge dev ui.observe VM --request window.json > before.json
```

`data.screenshot` 使用[截图契约](DEVELOPER_SCREENSHOTS.md)，含有界 PNG、实际捕获 `rect`、时间、尺寸和哈希。`data.source` 为 `visual` 或 `uia-and-visual`；`data.uia.status` 为：

- `available`：携带查询 `data` 和它的 `complete`。
- `unavailable`：仅给稳定 `error_code`，图像仍保留。
- `not_requested`：请求设置了 `include_uia:false`。

UIA 超时、不支持或搜索不完整不能解释成“没有控件”。图像与 UIA 是相邻的独立观察，各有时间，不承诺原子快照。

`data.observation` 应原样传回：它含 Windows 当前用户作用域的 DPAPI token，绑定窗口身份、会话、DPI、图片哈希/尺寸、实际捕获 `rect` 和独立的 Win32 `window_rect`。token 不写入 guest 文件，硬有效期为 30 秒；它防止元数据误改，不防范控制同一 Windows 用户的代码。过期后重新观察，不要修改时间戳。

## 从客户端候选生成点击请求

`target` 只接受一个点，或同时含 `width`、`height` 的矩形；矩形点击中心，不接受候选数组。坐标原点为候选图像左上角，单位为图像像素。省略 `image_width` / `image_height` 时使用原图尺寸；若客户端缩放了整张图，应给出缩放后的尺寸。不能直接传入裁剪、加边框或拼接图片上的坐标，也不额外乘 DPI 系数。

以下脚本把实际 `before.json` 中的观察对象与一个合成候选组合成完整 `click.json`。坐标应先由客户端按本次图片确认；不要手写或复用其他窗口的 token：

```sh
python3 - <<'PYCLIENT'
import json
from pathlib import Path
before = json.loads(Path("before.json").read_text(encoding="utf-8"))
if before.get("ok") is not True:
    raise SystemExit("需要成功的 ui.observe 或 ui.click 响应")
request = {
    "observation": before["data"]["observation"],
    "target": {"x": 120, "y": 40},
    "focus": False,
    "max_age_ms": 15000,
    "include_uia": True
}
Path("click.json").write_text(json.dumps(request, ensure_ascii=False), encoding="utf-8")
PYCLIENT
./bin/starbridge --dry-run dev ui.click VM --request click.json --allow-write
```

确认候选、窗口身份和焦点后，实际点击使用 `./bin/starbridge dev ui.click VM --request click.json --allow-write`。若模型定位已超过有效期，先重新观察。需要请求普通前台激活时显式设置 `focus:true`；默认不切换焦点。

项目也提供[完整 Python 客户端](../examples/visual-flow.py)，负责生成请求、保存 PNG 并传回 token，不包含模型/OCR。以下身份与位置均为合成示例，实际执行前需替换：

```sh
python3 examples/visual-flow.py --bridge ./bin/starbridge observe VM --hwnd 0x123 --pid 42 \
  --start-time-utc 2026-01-02T03:04:05Z --output work/visual-before
# 查看 work/visual-before/screenshot.png，在有效期内选择一个候选：
python3 examples/visual-flow.py --bridge ./bin/starbridge click VM \
  --observation work/visual-before/result.json --x 120 --y 40 \
  --focus --output work/visual-after
```

`--output` 必须是新目录。客户端在可能输入前独占创建 0700 目录和 0600 结果文件，拒绝覆盖旧观察；点击显式传入 `--allow-write`。如客户端超时或输出失败，先检查应用，不要假定未点击。

## 校验、预算与结果

`ui.click` 在同一 Windows 会话互斥锁内核对 token、年龄、HWND/PID/进程启动时间、会话、Win32 几何与 DPI。可选聚焦后再次校验，然后复用 `input.mouse` 核心检查前台、客户区、遮挡和物理输入状态。一个外层锁覆盖聚焦、点击和点击后的截图，不会嵌套申请同一锁。

默认只验证几何与新鲜度，不保证图像内容未变。`strict_image_match:true` 额外按原捕获方法复拍，比较完整 PNG 哈希；动画、光标、hover 或聚焦导致的变化均可能拒绝。客户端仍需判断候选是否足够明确。

- `include_uia`：默认 `true`；固定查询上限 64 项、500 个扫描节点、深度 6，不读取控件值。
- `timeout_ms`：默认 `15000`，范围 `2000..20000`。`uia_timeout_ms` 默认 `1500`，范围 `1000..5000`，必须小于总 timeout。可选 UIA 使用视觉 worker 结束后的剩余等待预算，不足 1000ms 时降级为 `UIA_TIMEOUT`。
- `max_age_ms`：点击默认 `15000`，范围 `100..30000`，按 Windows guest 时钟计算。
- `wait_ms`：点击后的采样延迟，默认 `250`，范围 `0..2000`。
- `lock_timeout_ms`：默认 `0`，范围 `0..5000`；与 `wait_ms` 之和必须小于 `timeout_ms`。
- `button`：`left`（默认）、`right` 或 `middle`。坐标经映射后仍需在物理输入的 `-65535..65535` 范围内。
- `max_bytes`、`max_pixels` 沿用截图预算。`capture_method` 可选 `print_window` / `windows_graphics_capture`；点击省略时继承前次观察的后端。可先调用 `screenshot.capabilities`。

进程启动、清理与传输还受外层 CLI 超时影响；超时约束等待，不证明输入已撤销。

点击成功只表示 `outcome:"dispatched"`，`coordination:"windows-session-mutex"`，`verification.status` 始终为 `pending`；另返回实际输入点和新的观察、PNG、可选 UIA。`wait_ms` 不是业务完成条件，应通过新图片、`ui.query` 或[等待/断言](DEVELOPER_WAIT.md)验证结果。

Windows worker 能确认尚未开始时返回 `error.details.outcome:"not_started"`；本地参数或授权错误可能更早返回。聚焦或输入开始后失败、worker/传输超时、后截图失败，以及点击提交后无法验证响应时，都不能当作未发生，`unknown` 表示需要重新观察。桥接层不会撤销或自动重试输入。

## MCP 授权与边界

开启 `serve --developer-tools` 可调用 `dev.ui.observe`；点击另需 `--allow-guest-write`，不需要宿主 workspace。成功返回原生 MCP `image/png`，文字与 structured content 只保留元数据。

配置动作策略时，按同一 VM UUID 检查全部依赖：

- `ui.observe`：`dev.ui.observe`、`dev.screenshot.capture`；默认还需 `dev.ui.query`。
- `ui.click`：`dev.ui.click`、`dev.screenshot.capture`、`dev.input.mouse`；默认还需 `dev.ui.query`，`focus:true` 另需 `dev.window.focus`。

`include_uia:false` 不要求 UIA grant。拒绝授权时不调用 guest；策略针对 UUID 已能判断拒绝时也不枚举 VM。读取仍可能与其他客户端写入交错，会话锁不排斥人工输入，也不锁定应用内容。

Go 测试使用生成 PNG、fake transport 和真实 MCP SDK；`tests/windows/developer-visual-contract.ps1` 使用合成 native/input 替身与真实 DPAPI，检查映射、篡改、过期、会话/DPI/几何变化、单次派发、后截图失败和 UIA 降级，不操作真实桌面。任意应用渲染、焦点和命中仍需[开发验收](DEVELOPER_ACCEPTANCE.md)。
