# Orchestrator Worker Image

هذه صورة تشغيل مخصصة لمسار Claude-led Orchestrator. تحتوي على:

- Go toolchain.
- Node.js وnpm وpnpm.
- Git وOpenSSH client.
- لا تحتوي على مفاتيح API أو credentials.
- تعمل افتراضيًا بالمستخدم غير الجذر `ao-worker`.
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
  --tmpfs /tmp:rw,nosuid,nodev,size=64m `
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
  --tmpfs /tmp:rw,nosuid,nodev,size=64m `
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
