<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/assets/droidline-logo-dark.png">
    <img src="docs/assets/droidline-logo.png" alt="Droidline" width="300">
  </picture>
</p>

# Droidline

[English](README.md) · **한국어** · [简体中文](README.zh-CN.md)

안드로이드 폰을 어떤 언어에서든 동작 하나에 한 줄로 제어합니다. ADB도, USB 케이블도, 루팅도 필요 없습니다.

```python
from droidline import connect

d = connect()                                   # 이 PC에 등록된 폰
d.launch("com.kakao.talk")
d.dump("screen.json")                           # 여기서 id와 글자를 확인
d.touchById("com.kakao.talk:id/login")
d.input("id", "com.kakao.talk:id/email", "knife")
d.sendkey("enter")
if d.exists("text", "광고 닫기"):
    d.touch("text", "광고 닫기")
d.tap(540, 1200)                                # 좌표는 tap만
d.batch([("airplane", True), ("sleep", 3000), ("airplane", False)])  # IP 갱신
```

사이트와 문서: <https://droidline.dev/ko/>

## 동작 방식

```
내 코드 ── SDK / CLI / MCP / curl ──► droidline serve (PC) ◄── Droidline 앱 (폰)
              localhost:8780                               폰이 먼저 접속
```

* 폰에는 앱 하나만 설치합니다. 접근성 서비스, 자체 키보드, 필요할 때만 쓰는 앱별 VPN이 들어 있습니다. 폰이 PC로 먼저 접속하므로 폰에서 포트를 열 일도, `adb forward`를 쓸 일도 없습니다.
* PC에서는 `droidline serve`를 실행합니다. 내 코드는 `localhost:8780`에 명령 한 줄씩 보냅니다.
* 대기, 재시도, 좌표 대체는 폰과 서버가 처리합니다. `touch`는 대상이 뜰 때까지 최대 10초 기다리고, 노드 클릭이 거부되면 그 노드의 bounds 중심을 좌표로 탭합니다.
* SDK 함수, 통신 명령, CLI 하위 명령, MCP 툴의 이름이 모두 같습니다. 전부 [`spec/commands.json`](spec/commands.json) 한 파일에서 만듭니다.

폰은 같은 와이파이, 포트포워딩한 PC, 터널, 또는 직접 배포한 릴레이를 거쳐 모바일 데이터로도 붙을 수 있습니다. 핸드셰이크 이후의 모든 줄은 종단간 암호화되므로 릴레이나 터널은 암호문만 전달합니다. 자세한 규격: [`spec/PROTOCOL.md`](spec/PROTOCOL.md).

## 빠른 시작

1. **PC 서버.** [Releases](https://github.com/KnifeLemon/Droidline/releases)에서 OS에 맞는 `droidline`을 받거나, Go 1.26 이상으로 빌드합니다.
   ```bash
   go install github.com/KnifeLemon/Droidline/server/cmd/droidline@latest
   ```
   실행한 뒤 켜 둡니다.
   ```bash
   droidline serve
   ```
2. **폰 앱.** [Releases](https://github.com/KnifeLemon/Droidline/releases)에서 `droidline-agent.apk`를 설치합니다. 구글 플레이에는 올리지 않습니다. 앱을 열고 체크리스트(접근성, Droidline 키보드, 알림 권한, 배터리 최적화 예외)를 따라 하세요. Android 13 이상에서는 사이드로드한 앱의 접근성을 켜기 전에 앱 정보 화면에서 "제한된 설정 허용"을 먼저 눌러야 하며, 앱이 위치를 안내합니다.
3. **페어링.** PC에서:
   ```bash
   droidline pair
   ```
   앱으로 QR 코드를 스캔합니다. 카메라가 없으면 앱에서 "이 와이파이에서 페어링"을 누르고, 폰에 뜬 6자리 코드를 PC에 입력합니다: `droidline pair 482913`.
4. **첫 명령.**
   ```bash
   droidline touch text "설정"
   ```

폰이 없다면 `droidline-fakephone`이 데모 앱이 든 가상 폰을 띄웁니다. SDK, CLI, MCP를 먼저 써 볼 수 있습니다.

```bash
go install github.com/KnifeLemon/Droidline/server/cmd/droidline-fakephone@latest
droidline-fakephone            # 코드가 뜨면 droidline pair <코드>
droidline launch dev.droidline.demo
```

## 언어별 사용

| 인터페이스 | 설치 | 예 |
|---|---|---|
| Python | `pip install droidline` | `connect().touch("text", "확인")` |
| Node.js / TypeScript | `npm install droidline` | `await (await connect()).touch("text", "확인")` |
| CLI | 서버에 포함 | `droidline touch text 확인` |
| HTTP | 설치 없음 | `curl -X POST localhost:8780/devices/_/touch -H 'content-type: application/json' -d '{"by":"text","value":"확인"}'` |
| MCP | 서버에 포함 | `droidline mcp` |
| 그 외 모든 언어 | TCP 소켓 | `{"id":1,"cmd":"touch","by":"text","value":"확인"}`을 보내고 한 줄을 읽기 |

MCP 클라이언트 설정(Claude Desktop, Claude Code, Cursor 등):

```json
{ "mcpServers": { "droidline": { "command": "droidline", "args": ["mcp"] } } }
```

## 좌표 대신 요소를 집기

`dump()`는 화면을 트리로 돌려줍니다. 각 노드에 `text`, `id`, `desc`, `class`, `bounds`가 있고, 이 이름을 그대로 첫 번째 인자로 씁니다.

| `by` | 비교 대상 | 줄임 이름 |
|---|---|---|
| `text` | text 완전 일치 | `touchByText` |
| `textContains` | text 부분 일치 | |
| `id` | resource-id. `"login"`만 써도 `"<패키지>:id/login"`과 일치 | `touchById` |
| `desc` | content-desc 완전 일치 | `touchByDesc` |
| `descContains` | content-desc 부분 일치 | |
| `class` | class 이름, 보통 `nth`와 함께 | |

`exists`, `which`, `checked`, `get_text`, `in_app` 같은 확인 명령은 기다리지 않고 바로 답하며, 대상이 없어도 에러를 내지 않습니다. 그래서 `if`문에 그대로 넣을 수 있습니다.

## 안 되는 것

Droidline은 일반 앱이 받을 수 있는 권한만 씁니다. 그래서 못 하는 일이 있고, 미리 알아 두는 편이 낫습니다.

* 다른 앱이 공개하지 않은 화면(액티비티)은 열 수 없습니다. `launch`는 그 앱의 시작 화면으로 대신 엽니다.
* 강제 종료, 데이터 삭제, 모바일 데이터·와이파이·비행기 모드 전환은 설정 화면을 열어 버튼을 대신 누르는 방식입니다. 한 번에 몇 초 걸리고, 제조사마다 버튼 문구가 다릅니다. 기준 기종은 삼성과 픽셀입니다.
* 게임, 일부 웹뷰, 커스텀 UI에는 접근성 노드가 없습니다. 이런 화면에서는 `tap` 좌표와 `color(x, y)`를 씁니다.
* Android 15 이상은 알림 속 인증번호를 앱에게 가립니다. `wait_notification`으로 도착은 알 수 있고, 번호는 앱을 열어 `get_text`로 읽습니다.
* 앱별 프록시는 Android 10 이상에서 쓸 수 있고, 폰에는 VPN이 하나만 켜집니다. 시스템 프록시 설정을 무시하는 앱은 프록시를 우회하지 않고 연결이 막힙니다.
* `sendkey`로 키코드나 글자를 보내는 일과 클립보드 읽기는 Droidline 키보드가 현재 입력기여야 합니다.

## 저장소 구성

| 경로 | 내용 |
|---|---|
| [`spec/`](spec) | `commands.json`(모든 명령), `PROTOCOL.md`, 암호화 테스트 벡터 |
| [`server/`](server) | Go: `droidline`(서버, CLI, MCP), `droidline-relay`, `droidline-fakephone` |
| [`agent/`](agent) | 안드로이드 앱(Kotlin) |
| [`sdk/python`](sdk/python), [`sdk/node`](sdk/node) | 공식 SDK, 명령 정의에서 생성한 코드와 얇은 클라이언트 |
| [`relay/worker`](relay/worker) | Cloudflare Workers용 릴레이 |
| [`scripts/gen.mjs`](scripts/gen.mjs) | `commands.json`에서 SDK 메서드를 생성 |

## 현재 상태

0.1 버전이며 첫 공개 릴리스 전입니다. 서버, CLI, MCP 어댑터, 릴레이, 두 SDK는 가상 폰을 상대로 테스트를 통과했습니다. 안드로이드 앱은 빌드와 단위 테스트를 통과했고, 제조사별 실기기 테스트가 다음 단계입니다. 특히 설정 매크로는 실제 폰에서 온 제보가 필요합니다. 무엇을 어떻게 확인했는지는 [`docs/STATUS.md`](docs/STATUS.md)에 있습니다.

보안 문제는 [SECURITY.md](SECURITY.md)를 보세요. Droidline은 본인 소유이거나 자동화 허락을 받은 폰과 계정에만 쓰세요. 용도는 [이용 약관](https://droidline.dev/ko/terms/)에 적어 두었습니다.

## 라이선스

MIT. [LICENSE](LICENSE) 참고.
