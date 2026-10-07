# دليل التشغيل: Claude يخطّط ويراجع، والنماذج الأخرى تكتب الكود

## كيف يعمل التشغيل الواحد

1. ترسل مهمة إلى الـAPI المحلي للـdaemon.
2. **Claude يخطّط:** يقسم المهمة إلى مهام فرعية، ويحدد لكل منها النموذج الذي يكتب كودها (`deepseek` أو `claude`).
3. **worktree لكل تشغيل:** يُنشأ فرع `ao/claude-orchestrator/<runId>` في worktree تحت `~/.ao/worktrees/claude-orchestrator/`. نسختك الرئيسية لا تُلمس.
4. **النموذج يكتب الكود** لكل مهمة فرعية على خطوتين: يختار الملفات التي يريد قراءتها، ثم يقترح تعديلات كاملة على الملفات بصيغة JSON. تُفحص التعديلات قبل تطبيقها: لا مسارات خارج الـworktree، ولا روابط رمزية، ولا `.git`، ولا ملفات أسرار، مع حدود للحجم والعدد.
5. **التحقق:** تُشغَّل أوامر العامل الثابتة ثم أوامر المدقّق داخل الـworktree، على المضيف أو في حاوية Docker معزولة.
6. **إعادة المحاولة:** إذا فشلت المحاولة يُعاد إرسال سبب الفشل إلى النموذج، حتى عدد `maxRetries`.
7. **Claude يراجع** النتيجة ويوصي بـ`merge` أو `hold`.
8. **حفظ العمل:** تُسجَّل التغييرات في commit على فرع التشغيل، أياً كانت التوصية. لا يحدث دمج ولا push تلقائياً.

## الإعداد

تُقرأ المتغيرات عند تشغيل الـdaemon. لذلك ضعها في بيئة المستخدم ثم أعد تشغيل تطبيق AO. **لا تضع مفاتيح API في أي ملف داخل المستودع.**

مثال على Windows (PowerShell). يحفظ `setx` القيم لجلسات تسجيل الدخول القادمة:

```powershell
setx AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED "on"

# Claude: المخطط والمراجع، عبر واجهة Anthropic المتوافقة مع OpenAI
setx AO_CLAUDE_ORCHESTRATOR_CLAUDE_BASE_URL "https://api.anthropic.com/v1"
setx AO_CLAUDE_ORCHESTRATOR_CLAUDE_MODEL "claude-opus-5-5"
setx AO_CLAUDE_ORCHESTRATOR_CLAUDE_API_KEY "<مفتاح Anthropic>"

# DeepSeek: العامل الافتراضي الذي يكتب الكود (تحقّق من القيم في توثيق DeepSeek)
setx AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_BASE_URL "https://api.deepseek.com/v1"
setx AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_MODEL "deepseek-chat"
setx AO_CLAUDE_ORCHESTRATOR_DEEPSEEK_API_KEY "<مفتاح DeepSeek>"

# المستودع الذي يعمل عليه الـorchestrator
setx AO_CLAUDE_ORCHESTRATOR_WORKER_PROJECT_ROOT "C:\path\to\your\repo"

# أوامر ثابتة يحددها المشغّل وحده، بصيغة argv بلا shell
setx AO_CLAUDE_ORCHESTRATOR_WORKER_COMMANDS '[{"name":"build","argv":["go","build","./..."]}]'
setx AO_CLAUDE_ORCHESTRATOR_VALIDATOR_COMMANDS '[{"name":"test","argv":["go","test","./..."]}]'

# مهلة المحاولة الواحدة تشمل استدعاءَي النموذج والأوامر
setx AO_CLAUDE_ORCHESTRATOR_WORKER_TIMEOUT "15m"
setx AO_CLAUDE_ORCHESTRATOR_VALIDATOR_TIMEOUT "10m"
```

**إعدادات اختيارية:**

| المتغير | الافتراضي | الوظيفة |
|---|---|---|
| `AO_CLAUDE_ORCHESTRATOR_WORKER_DEFAULT_PROVIDER` | `deepseek` | النموذج الذي يكتب الكود عندما لا تحدده الخطة (`claude` أو `deepseek`) |
| `AO_CLAUDE_ORCHESTRATOR_<CLAUDE\|DEEPSEEK>_TIMEOUT` | `3m` | مهلة الطلب الواحد إلى النموذج |
| `AO_CLAUDE_ORCHESTRATOR_<CLAUDE\|DEEPSEEK>_MAX_TOKENS` | 4096 | الحد الأقصى لطول ردّ النموذج. ارفعه إن كانت الملفات كبيرة |
| `AO_CLAUDE_ORCHESTRATOR_WORKER_WORKTREE_PATH` | فارغ | يثبّت كل التشغيلات على worktree موجود بدل إنشاء واحد لكل تشغيل |
| `AO_CLAUDE_ORCHESTRATOR_WORKER_SANDBOX_*` | معطّل | تشغيل الأوامر في Docker. انظر `docker/orchestrator-worker/README.md` |

إذا كان أي إعداد إلزامي ناقصاً يرفض الـdaemon البدء، ويذكر أسماء المتغيرات الناقصة دون قيمها.

## تشغيل مهمة

الـAPI متاح محلياً فقط (`127.0.0.1:3001` افتراضياً، ويتغير بـ`AO_PORT`). يرفض أي طلب يحمل ترويسة `Origin`، لذلك يُستدعى من الطرفية وليس من المتصفح:

```powershell
$run = Invoke-RestMethod -Method Post http://127.0.0.1:3001/internal/claude-orchestrator/runs `
  -ContentType 'application/json' `
  -Body '{"task":"أضف اختباراً لدالة parseConfig","explicitOptIn":true,"maxRetries":2}'

# متابعة الحالة حتى تنتهي: pending ثم planning ثم executing ثم validating ثم reviewing، وبعدها completed أو held أو failed
Invoke-RestMethod http://127.0.0.1:3001/internal/claude-orchestrator/runs/$($run.runId)

# إلغاء التشغيل
Invoke-RestMethod -Method Post http://127.0.0.1:3001/internal/claude-orchestrator/runs/$($run.runId)/cancel
```

عند انتهاء التشغيل يعيد الـAPI أربعة أشياء: `branch`، و`commit`، و`recommendation` (`merge` أو `hold`)، والحالة. لا يعيد أي نص من النموذج أو الأوامر.

لمراجعة العمل ودمجه بنفسك:

```powershell
git log --stat ao/claude-orchestrator/<runId>
git diff main...ao/claude-orchestrator/<runId>
git merge ao/claude-orchestrator/<runId>   # إن وافقت على النتيجة
```

بعد الانتهاء من الـworktree احذفه:

```powershell
git worktree remove <path>
git branch -D ao/claude-orchestrator/<runId>
```

## الحدود والضمانات

- **التشغيلات المتزامنة:** الحد تشغيلان. التشغيل الثالث يُرفض بالرمز `429 TOO_MANY_ACTIVE_RUNS`.
- **ما يراه النموذج:** لا يرى أي مسار محلي، ولا ملفات `.git` أو الأسرار. ويستحيل عليه اختيار مكان تنفيذ الأوامر.
- **حدود كل محاولة:**
  - **القراءة:** 20 ملفاً، حتى 128KB للملف و512KB إجمالاً.
  - **الكتابة:** 50 تعديلاً، حتى 512KB للملف و4MB إجمالاً.
- **الأوامر:** يحددها المشغّل فقط، ولا تُبنى أبداً من مخرجات النموذج. وتُنفَّذ دون shell.
- **ملاحظة أمان:** بدون Docker تعمل أوامرك على جهازك مباشرة. ويمكن أن تشغّل ملفات الاختبار التي كتبها النموذج، وهذا يعني تنفيذ كود كتبه النموذج على جهازك. لذلك فعّل الـsandbox للمستودعات المهمة.
- **مدة حفظ الحالة:** تُحفظ في الذاكرة فقط، لآخر 256 تشغيلاً. بعد إعادة تشغيل الـdaemon تضيع حالة التشغيلات، لكن الفروع والـcommits تبقى في git.

## حالة الجاهزية

| الجزء | الحالة |
|---|---|
| التخطيط والمراجعة عبر Claude، والكتابة عبر DeepSeek أو Claude | ✅ مبني ومختبر من طرف لطرف بمزوّدين مزيّفين عبر HTTP حقيقي |
| worktree وفرع لكل تشغيل مع commit للنتيجة | ✅ مختبر على git حقيقي |
| أمان تطبيق التعديلات (المسارات والروابط والأسرار والحدود) | ✅ مختبر، مع اختبار يتأكد أن الحماية لا يمكن إزالتها دون أن يفشل |
| الـsandbox وصورة Docker | ✅ CI يبني الصورة ويتحقق من العزل وملكية الملفات |
| CI | ✅ lint والبناء والاختبارات الكاملة ناجحة |
| تشغيل بمفاتيح حقيقية | ⏳ يحتاج مفاتيحك على جهازك، ولم يُجرَّب هنا |
| واجهة سطح المكتب | ❌ غير موصولة. يُستخدم الـAPI من الطرفية، لأن المسارات الداخلية ترفض طلبات المتصفح عمداً، وربطها يحتاج جسراً عبر عملية Electron الرئيسية |
| تخزين دائم في SQLite | ❌ مؤجل عمداً (انظر `06-70-percent-verification.md`) |
