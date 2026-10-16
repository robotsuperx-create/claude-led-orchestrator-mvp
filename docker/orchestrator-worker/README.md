# Orchestrator Worker Image

هذه صورة تشغيل مخصصة لمسار Claude-led Orchestrator. تحتوي على:

- Go toolchain.
- Node.js وnpm وpnpm.
- Git.
- لا تحتوي على مفاتيح API أو credentials.
- تعمل افتراضيًا بالمستخدم غير الجذر `ao-worker`. على Linux يشغّلها الـOrchestrator بمستخدم المضيف (`--user uid:gid`) حتى تبقى ملفات الـworktree قابلة للكتابة ومملوكة لك.
- كل مجلدات `HOME` والكاش (Go وnpm) موجّهة إلى `/tmp`، وهو tmpfs بحجم 1GB. لا يُكتب أي شيء في مستودعك غير ما تغيّره الأوامر نفسها.
- `GOTOOLCHAIN=local` لأن الشبكة مقطوعة. على المشروع أن يضع تبعياته في `vendor/`، أو أن تبني صورة مشتقة فيها كاش الوحدات جاهز، وإلا سيفشل `go test` لأنه لا يستطيع تنزيل الوحدات.
- `GOFLAGS=-buildvcs=false` لأن ملف `.git` في الـworktree يشير إلى مسار على المضيف غير مركّب داخل الحاوية.
- لا تمنح الحاوية أي صلاحية Docker socket.

## البناء

من جذر المستودع:

```powershell
docker build --pull -f docker/orchestrator-worker/Dockerfile -t ao/worker:v1 .
docker image inspect ao/worker:v1
```

يجب تنفيذ `--pull` أثناء البناء فقط لتحديث صورة الأساس. أثناء التشغيل يستخدم الـOrchestrator:

```text
--pull=never
```

حتى لا يسمح بتغيير الصورة أثناء تشغيل مهمة.

## اختبار الصورة دون شبكة

```powershell
docker run --rm `
  --network none `
  --read-only `
  --cap-drop ALL `
  --security-opt no-new-privileges `
  --memory 256m `
  --cpus 1 `
  --pids-limit 128 `
  --tmpfs /tmp:rw,nosuid,nodev,size=1g `
  --entrypoint /bin/echo `
  ao/worker:v1 `
  sandbox-ok
```

اختبار منع الشبكة:

```powershell
docker run --rm `
  --network none `
  --read-only `
  --cap-drop ALL `
  --security-opt no-new-privileges `
  --memory 256m `
  --cpus 1 `
  --pids-limit 128 `
  --tmpfs /tmp:rw,nosuid,nodev,size=1g `
  --entrypoint /bin/sh `
  ao/worker:v1 `
  -c "node -e \"require('https').get('https://example.com',()=>process.exit(0)).on('error',()=>process.exit(1))\" && echo network-open || echo network-blocked"
```

النتائج المطلوبة:

```text
sandbox-ok
network-blocked
```

## استخدامه مع الـOrchestrator

```powershell
$env:AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_ENABLED="true"
$env:AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_IMAGE="ao/worker:v1"
$env:AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_MEMORY_BYTES="268435456"
$env:AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_NANO_CPUS="1000000000"
$env:AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_PIDS="128"
```

لا تُضمّن أي API key داخل الصورة. مفاتيح المزودين يجب أن تُدار خارج الصورة، ولا تُمرر إلى worker إلا عبر allowlist صريحة وبعد إضافة سياسة egress مناسبة.

## التحقق في CI

يبني workflow `orchestrator-worker-image.yml` الصورة عند أي تغيير فيها أو في `sandboxrunner`، ويتحقق من أربعة أمور بنفس قيود العزل: الحاوية تعمل، والشبكة محجوبة، وأدوات Go تعمل دون شبكة، والملفات المكتوبة في الـworktree مملوكة لمستخدم المضيف.

