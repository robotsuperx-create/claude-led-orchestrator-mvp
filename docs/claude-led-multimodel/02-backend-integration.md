# تكامل الخلفية: Claude-led Multi-model

**الحالة:** عقد وخدمة أولية وعميل HTTP موجودة في شجرة Go، لكن التكامل غير موصول بعملية `daemon` أو بواجهة HTTP. هذه الوثيقة تصف ما هو موجود فعلياً وما يلزم قبل تشغيله؛ لا تفترض أن وجود الحزم يعني أن الميزة متاحة للمستخدم.

## 1. حدود المنافذ والمسؤوليات

المبدأ هو أن منطق التطبيق يعتمد على واجهات `backend/internal/ports`، لا على SDK أو نقل HTTP أو التخزين المباشر. تفاصيل العقود موجودة في [`ports/claude_orchestrator.go`](../../backend/internal/ports/claude_orchestrator.go).

| الحد | العقد الحالي | المسؤولية والقيود |
|---|---|---|
| تنفيذ دورة التشغيل | `ports.ExecutiveOrchestrator` | `Run(ctx, OrchestrationRequest)` ينسّق تشغيل orchestration. |
| التخطيط والمراجعة | `ports.ModelGateway` | `Plan` و`Review` يستقبلان طلبات ونتائج typed؛ لا يملكان دورة Chat أصلية أو سجل محادثة أو approvals أو resume. |
| تنفيذ المهام | `ports.WorkerRuntime` | `Execute` ينفّذ محاولة لمهمة مفوّضة. تفاصيل العملية وruntime تخص تنفيذ المنفذ، لا `ports`. |
| التحقق | `ports.Validator` | `Validate` يقرر اجتياز نتائج العمل؛ لا يفرض أداة أو مشغّل أوامر بعينه. |
| سياق ونتيجة المشروع | `ports.ProjectMemory` | `ReadContext` و`RecordOutcome` يحددان حد القراءة والتسجيل، لكنهما لا يفرضان قاعدة بيانات أو مدة احتفاظ. |
| تطبيق orchestration | `service/claudeorchestrator.Service` | ينسّق المنافذ الأربعة؛ لا ينفذ طلبات مزود مباشرة، ولا يكتب SQLite أو يغير worktree أو ينفذ Git merge. |
| نقل نماذج خام | `adapters/modelgateway.Client` | عميل مساعد لـ`OpenAI-compatible chat-completions`، وليس بحد ذاته تطبيقاً لـ`ports.ModelGateway`. |
| Chat الأصلي | `ports.ChatDriver` / `service/chat` | حد منفصل للمحادثات الأصلية طويلة العمر. لا تستبدله ببوابة الطلبات المنفصلة؛ لا تنقل history أو handoff أو approvals عبر هذا التكامل. |

حقول `ModelProvider` المعرفة هي `claude`, `deepseek`, `kimi`, `gemini`, و`openai`. لكن خدمة orchestration الحالية تمرر `ModelProviderClaude` صراحةً إلى `Plan` و`Review`؛ وجود بقية الأسماء في enum **لا يعني** وجود adapters أو توجيه متعدد المزودين. قد يحدد التخطيط `Provider` لكل `PlannedSubtask`، لكن لا يوجد `WorkerRuntime` موصول ينفذ هذا التوجيه حالياً.

## 2. إضافة adapter جديد

اتبع اتجاه الاعتماد `service → ports ← adapters`، وأبقِ أنواع الطلب/النتيجة مستقلة عن SDK المزود:

1. **نفّذ المنفذ المطلوب أولاً:** لكي يصبح التخطيط/المراجعة قابلاً للاستخدام، يجب توفير تطبيق فعلي لـ`ports.ModelGateway`. لا يكفي إضافة عميل HTTP يرسل نصاً؛ يجب تحويل نتيجة المزود إلى `ExecutionPlan` أو `ReviewDecision` typed والتحقق منها قبل إعادتها. أضف `WorkerRuntime` أو `Validator` منفصلين فقط عند تنفيذ هذين الدورين.
2. **ضع تفاصيل المزود في `backend/internal/adapters/modelgateway/<provider>`** (أو تقسيم متفق عليه داخل adapters)، ولا تضف SDK أو أنواع مزود خاصة إلى `ports` أو `service/claudeorchestrator`.
3. **استخدم `context.Context` لكل I/O.** احترم `ctx` والإلغاء والمهلة، وصنّف أخطاء النقل/المزود من دون إرفاق body أو مفاتيح أو نص طلب حساس. لا تتبع redirect يمرر الاعتماد إلى مضيف آخر.
4. **تحقق من المخرجات والحدود:** قائمة مزود/نماذج مسموحة، حجم الطلب والرد، مهلة finite، حدود tokens/cost، schema للخطة والقرار، وعدد المهام وحدود إعادة المحاولة. لا تفترض أن نص JSON من LLM خطة موثوقة؛ الخدمة تتحقق حالياً من وجود subtasks و`ID` غير مكرر و`WorkerID`، لكن هذا ليس تحققاً أمنياً شاملاً من التعليمات أو صلاحيات العامل.
5. **أضف اختبارات عقد وتكامل محلية:** fake implementation لمنافذ الخدمة، و`httptest` لسلوك adapter (الطلب، المصادقة، timeout، cancellation، أخطاء HTTP، redirect، حدود الجسم، JSON المشوه، وعدم تسريب الرد الحساس). اختبر المدخلات العدائية وسجل الأخطاء الناتجة قبل اعتماد adapter.
6. **وصّل من نقطة التركيب:** أنشئ adapter والخدمة في `daemon`, معتمداً على config opt-in، ثم مرر التطبيق إلى dependency/controller مقصود. إذا أُضيفت API، أضف DTO/controller و`APIDeps` وroute ومصدر OpenAPI ثم ولّد الأنواع؛ لا تعدل `schema.ts` المولّد يدوياً. لا تجعل controller أو renderer يتصل بمزود أو يفتح SQLite مباشرة.

العميل الموجود في [`adapters/modelgateway/http_client.go`](../../backend/internal/adapters/modelgateway/http_client.go) يضيف `/chat/completions` إلى `BaseURL`، ويرسل `Authorization: Bearer` عند ضبط `APIKey`، ويقبل إعدادات timeout وحدود bytes. الافتراضيات الحالية هي `30s` و`1 MiB` للطلب و`4 MiB` للرد، ولا يتبع redirects. يقبل `http` أو `https` ويرفض user-info وquery وfragment في URL. هذه حدود نقل فقط؛ لا توفر وحدها allowlist للمضيفين، أو حماية SSRF، أو تشفير TLS إلزامياً، أو سقف tokens/cost، أو ترجمة إلى منفذ orchestration.

## 3. سياسة BYOK (Bring Your Own Key)

### ما يفعله الكود الآن

`modelgateway.Config` يحتوي `APIKey` اختياري، ويحتفظ به `Client` في الذاكرة ويرسله في ترويسة Bearer. `NewClient` يرفض CR/LF في المفتاح. لا يوجد حالياً مسار إعداد أو جلب أو تدوير مفتاح للمستخدم، ولا `SecretStore` مخصص، ولا توصيل للعميل بالـdaemon. لذلك لا تعني هذه الحقول أن BYOK مدعوم في المنتج.

### السياسة المطلوبة قبل توصيل BYOK

- يظل المفتاح ملكاً للمستخدم ويُستخدم فقط مع المزود والمضيف والنموذج الذين اختارهم ووافق على إرسال البيانات إليهم. وضّح أن `Task` و`MemoryContext` ونتائج العمال قد تغادر الجهاز؛ أرسل أقل سياق لازم، ولا ترفع workspace أو ملفات أو سجل Chat كاملاً تلقائياً.
- اجعل حفظ المفتاح في مخزن أسرار محلي/نظامي مخصص مع صلاحيات مناسبة. استخدم مرجعاً مثل `SecretRef` في الإعدادات والطلبات، لا المفتاح الخام؛ اقرأه في daemon عند الحاجة وقلل زمن بقائه في الذاكرة. لا تضعه في `Config` عامة قابلة للتسلسل أو `OrchestrationRequest` أو `MemoryOutcome` أو SQLite أو ملفات المشروع أو environment المحفوظة.
- لا تُرجع المفتاح إلى renderer أو هاتف أو endpoint قراءة. واجهة الإدخال، إن أضيفت، يجب أن تكون كتابة مقيدة لمصدر موثوق؛ لا تجعل CORS وحده حدّ صلاحية. لا تعرض endpoint إدارة اعتماد على LAN، حتى لو كان listener العام مضبوطاً بكلمة مرور.
- اسمح بمضيفات HTTPS معتمدة فقط، وقيّد الوصول إلى loopback إذا كان endpoint محلياً مسموحاً صراحةً. امنع عناوين metadata والشبكات الداخلية غير المصرح بها، وافحص DNS/redirect بما يمنع SSRF أو تسريب credential. العميل الحالي يقبل URL `http(s)` اعتباطياً، لذا لا تعتبره سياسة BYOK مكتملة.
- لا تسجل المفاتيح أو authorization headers أو request/response bodies أو prompts الحساسة في logs أو telemetry أو errors أو SSE/API أو crash reporting. لا تعتبر redaction بديلاً عن منع تسجيل السر.
- حدود الإنفاق، الاستخدام، النموذج، retention، والإلغاء تحتاج سياسة صريحة وموافقة قابلة للسحب. لا توجد حالياً محاسبة تكلفة أو ميزانية model-level في هذا المسار.

يوجد `claudeorchestrator.RedactSecrets` لاكتشاف أنماط شائعة، وتوجد له اختبارات؛ تعليقه نفسه يصفه بأنه **best-effort**. لا تستعمله كضمان لتخزين آمن: الخدمة لا تطبقه تلقائياً على جميع نصوص الأخطاء أو مخرجات المزود، وقد تدخل تفاصيل الفشل في `MergeDecision.Reasons` و`MemoryOutcome`. كما أن `HTTPError` الحالي يحجب body المزود، لكن adapters أخرى يجب أن تلتزم بالقاعدة نفسها.

## 4. Lifecycle التشغيل

عند استدعاء `Service.Run`، يسجل `runID` في خرائط ذاكرة العملية ويمنع تشغيل ID نفسه بالتوازي داخل نسخة الخدمة. التسلسل الحالي هو:

`pending → planning → executing → validating → reviewing → completed | held`

أي خطأ على مستوى التشغيل ينتج `failed` وقرار `hold`. يقرأ التطبيق `ProjectMemory`, يطلب الخطة، يتحقق من الحد الأدنى لصحتها، ينفذ subtasks بالتتابع مع `MaxRetries + 1` محاولات (وبحد أدنى محاولة واحدة)، يتحقق من النتائج، ثم يطلب مراجعة تنفيذية. تكون `MergeOutcomeMerge` توصية فقط عندما تنجح كل المهام ويمر التحقق وتوافق المراجعة؛ وإلا فالنتيجة `held` مع أسباب. لا ينفذ التطبيق merge.

`RunState` والـ`running` map في `service/claudeorchestrator.Service` داخل الذاكرة؛ يضيعان عند إعادة تشغيل العملية. واجهة `ProjectMemory.RecordOutcome` قد تسجل ملخصاً/حالة/قراراً، لكن لا يوجد تنفيذ موصول أو سجل تشغيل durable أو استعادة run أو idempotency/fencing في هذه الحزمة. حالات ومراحل `ports` عقد domain وليست API أو مخطط تخزين.

## 5. الإلغاء والمهل والإغلاق

كل منفذ يقبل `context.Context`، والـHTTP client يستخدم `http.NewRequestWithContext` وtimeout محدوداً؛ اختبارات adapter تغطي الإلغاء والمهلة. على أي تنفيذ جديد أن يمرر السياق نفسه إلى كل عملية بعيدة/محلية، وألا يترك retry أو عملية worker جارية بعد إلغاء السياق.

الحدود الحالية مهمة: لا توجد واجهة `Cancel(runID)` أو HTTP route للإلغاء أو سجل cancel handle. `executePlan` يفحص `ctx.Err()` بعد محاولة العامل ليوقف retries، لكنه لا يخرج فوراً من حلقة subtasks، ولا يوقف `Run` صراحةً قبل validation/review. يعتمد الإيقاف الفوري على تعاون كل منفذ مع السياق، وهو غير مضمون قبل وجود implementations واختبارات لها. يسجل `record` النتيجة عبر `context.WithoutCancel(ctx)` عمداً كيلا يمنع الإلغاء حفظ النتيجة النهائية؛ لذلك يجب أن يبقى التسجيل محدوداً وغير حامل للأسرار.

قبل توصيل الخدمة بالـdaemon، اربط تشغيلاتها بـlifecycle daemon، حدد مهلة shutdown ووقت انتظار الأعمال، وأضف إلغاءً صريحاً إذا احتاج المنتج إلى التحكم في تشغيل بعينه. لا ترفع timeout طلب HTTP الحالي (افتراضياً `60s`) لجعل تشغيل طويل العمر يعمل داخل REST؛ التصميم غير المتزامن يحتاج عقد تشغيل/استعلام/إلغاء وحالة durable مصمماً صراحةً.

## 6. حدود المنافذ الشبكية وFeature flag

يؤكد [`config/config.go`](../../backend/internal/config/config.go) أن daemon الأساسي يربط `127.0.0.1` فقط، عادةً على `3001` ويمكن تغيير المنفذ بواسطة `AO_PORT`. لا تضف listener أو منفذاً عاماً خاصاً بـModel Gateway؛ اجعل حركة المزود الصادرة من daemon عبر adapter. يوجد في المنتج أيضاً LAN listener منفصل لبعض وظائف الهاتف؛ أي route جديدة قد تصل إليه عبر الراوتر المشترك بحسب سياسات الحجب والمصادقة. افحص مسار LAN الفعلي واختبره، ولا تعتمد على Host header أو CORS لحماية route أسرار.

**لا يوجد حالياً feature flag لـClaude orchestrator أو Model Gateway** في `config.Config` أو تحميل إعدادات daemon. كما لا يظهر في `daemon.Run` إنشاء `modelgateway.Client` أو `claudeorchestrator.Service`. عند التنفيذ، أضف flag backend صريحاً، مغلقاً افتراضياً وfail-closed؛ يجب أن يتحقق منه daemon/controller قبل قبول تشغيل أو استخدام credential، لا في الواجهة فقط. اجعل تعطيله يمنع تشغيلات جديدة ويوقف/يلغي القائمة وفق سياسة معلنة. الاسم، مصدر الإعداد، ومدى تفعيله لم تُحسم في الكود الحالي؛ لا تفترض متغير بيئة موجوداً.

## 7. ما لم يُوصل بعد

حتى شجرة Go التي فُحصت:

- لا يستورد `daemon` حزمتي `adapters/modelgateway` أو `service/claudeorchestrator` ولا يهيئهما.
- `APIDeps` و`httpd.API` لا يحتويان dependency أو controller لهذه الخدمة؛ لا توجد routes أو DTO أو OpenAPI أو UI موصولة لها.
- لا يوجد تطبيق فعلي لـ`ports.ModelGateway` يترجم `Plan`/`Review` عبر عميل completions؛ ولا `WorkerRuntime` أو `Validator` أو `ProjectMemory` إنتاجية موصولة لهذه العقود.
- enum يذكر خمسة أسماء مزودين، لكن توجد فقط طبقة HTTP عامة لـ`OpenAI-compatible chat-completions` في الملفات المفحوصة؛ لا تثبت توافق Claude API أو غيره، ولا تنفيذ تعدد المزودين.
- لا يوجد BYOK end-to-end أو SecretStore أو allowlist hosts/models أو ميزانية tokens/cost أو feature flag.
- لا يوجد run persistence أو استئناف أو endpoint حالة/إلغاء. لا تغيّر جلسات `Chat` الأصلية ولا تستخدم `reviewgateway` باعتباره Model Gateway؛ هما حدان مختلفان.

## 8. بوابة تحقق قبل الدمج

اختبارات Go المكتوبة لهذه العقود والعميل والخدمة هي:

```bash
cd backend
go test ./internal/ports ./internal/service/claudeorchestrator ./internal/adapters/modelgateway
```

تعذر تنفيذ الأمر أثناء إعداد هذه الوثيقة لأن `go` لم يكن متاحاً عبر `PATH`. في تحقق التكامل اللاحق استُخدم `/usr/local/go/bin/go` (الإصدار `go1.27.1`) واجتازت الحزم الثلاث اختبارات Go و`go vet`؛ راجع [`02-backend-verification.md`](02-backend-verification.md) للنتائج والحدود. لم يُشغّل `go test ./...` في تحقق التكامل. ما يزال مطلوباً إضافة اختبارات wiring للـdaemon والـfeature flag وHTTP/LAN/auth، واختبارات إلغاء الخدمة بأكملها وحماية الأسرار على مسار التخزين والأخطاء، لا مجرد اختبار helper مستقل.
