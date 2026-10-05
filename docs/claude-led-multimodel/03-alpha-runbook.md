# دليل تشغيل Alpha: Claude-led Multi-model

## الغرض والحالة

هذا الدليل يصف **Alpha هندسياً داخل شجرة المصدر**، لا ميزة جاهزة لمستخدم المنتج. عند الفحص وُجدت عقود وخدمة orchestration وطبقة إعداد مزودين، ومدير Git worktree، وValidator، وRun Gate، ومعالج HTTP معزول واختبارات محلية لها. أظهر `git status --short` أن بعض الإضافات ذات الصلة كانت **غير متتبعة** (منها `provider_config.go`، و`claude_orchestrator_api.go`، و`ports/worktree.go`، و`service/worktree/`، و`validator.go`، وملفات gate/E2E). أما العقود والخدمة الأساسية الموجودة مسبقاً فليست كلها ضمن تلك القائمة. في الحالتين، لا يظهر تركيب هذه الإضافات في daemon أو تسجيل معالج HTTP في مسارات المنتج؛ وجود الشيفرة لا يعني تشغيلها أو دمجها/إصدارها.

«70%» هنا **محطة قبول مستهدفة وليست نسبة إنجاز محسوبة**؛ لا توجد مقاييس أو مجموعة متطلبات موزونة تثبت رقماً كهذا. لا نعلن بلوغها إلا بعد قبول المراحل المحددة أدناه.

## تشغيل Fake E2E

المتطلبات: Go مثبت. في بيئة الفحص كان `go` غير ظاهر عبر `PATH`، واستُخدم `/usr/local/go/bin/go`. من جذر المستودع:

```bash
cd /home/ubuntu/agent-orchestrator-product/backend

# سيناريو Fake E2E بعينه، مع طباعة اسم الاختبار
/usr/local/go/bin/go test ./internal/service/claudeorchestrator \
  -run '^TestRunGatedEndToEndOptInRetryAndMerge$' -v

# حزمة اختبارات العقود والمكونات الأولية ذات الصلة
/usr/local/go/bin/go test \
  ./internal/ports \
  ./internal/service/claudeorchestrator \
  ./internal/adapters/modelgateway \
  ./internal/service/worktree \
  ./internal/httpd
```

في التحقق الذي أُجري لهذا الدليل، نجح الاختبار المحدد ونجحت الحزم الخمس أعلاه. هذه نتيجة **اختبارات محلية محددة فقط**؛ لم يُشغّل `go test ./...`، ولم يُختبر daemon كاملاً أو واجهة المنتج أو مزود حقيقي.

### ماذا يثبت Fake E2E؟

الاختبار `TestRunGatedEndToEndOptInRetryAndMerge` يستخدم doubles داخل العملية: `e2eModelGateway` و`e2eWorkerRuntime` و`e2eValidator` و`e2eProjectMemory`. يجرب رفض التشغيل دون الموافقة الصريحة، ثم يسمح بتشغيل اختباري بعد تفعيل السياسة والموافقة؛ ويثبت الترتيب `Plan → Delegate → Retry → Validate → Review → Merge`، ومحاولة عامل فاشلة يعقبها retry ناجح، وفحص validation، ومراجعة Claude، ثم تسجيل نتيجة نهائية في fake الذاكرة.

في السيناريو، التخطيط والمراجعة يطلبهما النموذج التنفيذي تحت `claude`، بينما يحمل subtask الموزع `deepseek`. لا يوجد استدعاء مزود أو مفتاح أو اتصال خارجي: هذه أسماء وطلبات typed داخل fake. وكلمة `Merge` في سجل الأحداث تعكس تسجيل outcome في `ProjectMemory` التجريبي؛ **الاختبار لا ينفّذ `git merge` ولا يغير worktree**. كما أنه لا يشغّل API أو UI أو SQLite أو مدير Worktree.

## العقود والسلوك الموجودان

### Worktree

- العقد `ports.WorktreeManager` يحدد `Create`, و`Remove`, و`Status` عبر أنواع طلب/نتيجة typed.
- التطبيق `service/worktree.Manager` يستخدم Git مباشرة بمتجه argv (`exec.CommandContext`)، لا shell. الإنشاء يطلب جذر مستودع Git، وفرعاً يمر بفحوص اسم، ومساراً غير موجود داخل جذر المشروع، وخارج `.git`؛ ويضيف worktree جديداً بفرع جديد.
- الإزالة عملية صريحة فقط؛ لا ينفذها `Create` أو `Status` أو مسار خطأ. خيار `Force` يمرر `--force` صراحةً. الحالة تقرأ `git status --porcelain=v1 --branch` بعد التأكد من أن المسار worktree مسجل للمشروع.
- اختبارات `manager_test.go` تحقن `fakeRunner`، وتغطي argv، ورفض مسارات/فروع معينة، والتأكد من عدم الإزالة الضمنية. لذلك لا تثبت وحدها سلوك Git حقيقي عبر مستودع مؤقت.
- هذا الحد يدير worktrees المحلية فقط؛ لا يطبّق patches ولا ينشئ commits ولا يدمج فرعاً ولا يربط نفسه بتشغيل orchestration أو lifecycle daemon.

### Validator

- `claudeorchestrator.NewValidator` يأخذ قائمة أوامر ثابتة ومسموحاً بها، وtimeout موجباً، و`CommandRunner` محقوناً. يرفض الإعداد غير الصالح وأوامر shell/interpreter المحظورة؛ ولا يستمد argv من task أو خطة النموذج.
- `ExecCommandRunner` يشغّل argv عبر `exec.CommandContext` دون shell. يحد Validator مهلة دورة الفحص كاملة، ويقيد stdout/stderr لكل منهما إلى `MaxValidationOutputBytes` (64 KiB) مع redaction best-effort، ويحوّل فشل الأمر إلى تقرير غير ناجح.
- حدود مهمة قبل الاستخدام: `Check` لا يستهلك حقول طلب التحقق (`ValidationRequest`) لتحديد الملفات أو الأمر؛ ينفذ **القائمة المضبوطة مسبقاً**. كما أن `ExecCommandRunner` الحالي لا يعيّن working directory إلى worktree بعينه، ولا ينشئ عزلاً أو sandbox. لا تعامل نجاحه على أنه تحقق من patch محدد أو منع من آثار جانبية خارجية.

### ProviderConfig وModel Gateway

- `modelgateway.ProviderConfig` يصف مزوداً مضبوطاً صراحةً: `Provider`, و`BaseURL`, و`DefaultModel`، مع قائمة نماذج اختيارية، وtimeout، وحد tokens. لا يقرأ environment variables ولا يخترع endpoint افتراضياً. القيم الافتراضية في الكود هي 30 ثانية و4096 token لمكالمة واحدة.
- `APIKey` موجود في الذاكرة وغير قابل للتسلسل عبر JSON (`json:"-"`)، وتمت إضافة تمثيلات `String`/`GoString` و`LogValue` تحجبه. هذه حماية من بعض مسارات الطباعة فقط، **وليست SecretStore أو BYOK إنتاجياً**.
- `NewRegistry` يقبل في الكود الحالي `claude` و`deepseek` فقط، مع BaseURL ونموذج افتراضي صريحين؛ ويمكن لـ`AllowedModels` تقييد اختيار النموذج. كلاهما يمر عبر تنسيق `OpenAI-compatible chat-completions`، بما في ذلك endpoint متوافق لـClaude؛ هذا لا يثبت توافق Native API أو بيانات اعتماد حقيقية. أسماء `kimi` و`gemini` و`openai` في enum لا تعني أن لها adapters هنا.
- المهايئ يطبق `ports.ModelGateway` ويحوّل Plan/Review إلى رسائل JSON متوقعة. اختبارات ProviderConfig محلية؛ لا تثبت اتصاله بمزود حقيقي أو وجود سياسة مضيفين موثوقة، SecretStore، موافقة egress، أو قياس تكلفة.

### Run Gate ونتيجة التشغيل

- `ClaudeOrchestratorRunPolicy` سياسة صغيرة ذات default-deny: قيمة `FeatureEnabled` الصفرية/false ترفض، وحتى عند تفعيلها يلزم `ExplicitOptIn: true` **لكل تشغيل**. الرفض له سبب typed (`feature_disabled` أو `explicit_opt_in_required`).
- `Service.RunGated` يفحص البوابة قبل `Run`. البوابة الغائبة أو رفضها أو أي خطأ منها يمنع بدء الخدمة؛ الرفض يرجع نتيجة `failed` مع قرار `hold` وسبب، ولا يسجل تشغيل orchestration. اختبارات البوابة وFake E2E تثبت هذه الحدود باستخدام fakes.
- قرار `merge` في `OrchestrationResult` توصية فقط؛ تطبيق الدمج يحتاج صلاحيات وخطوة Git منفصلة. حالة التشغيل والذاكرة اللتان يوفرهما التطبيق الحالي داخل العملية، وليستا سجل تشغيل دائم.

### API أولي، لا API موصول للمنتج

يوجد `backend/internal/httpd/claude_orchestrator_api.go` ومعه اختبارات. يعرّف POST لبدء تشغيل وGET لاستطلاع حالته، ويشترط طلباً محلياً وGate صالحاً وموافقة صريحة؛ ويقيد body إلى 16 KiB، وtask إلى 8 KiB، و`maxRetries` إلى 0–3، ولا يعيد النصوص الحساسة في الاستجابة. لكن `Register` لا يستدعي نفسه، والبحث في `backend/internal/daemon` ومسارات تسجيل API الحالية لم يظهر تركيب هذا المعالج. خريطة التشغيل داخله في الذاكرة؛ لا يوجد في هذا المسار حفظ دائم أو إلغاء API. لا توجد بذلك بعد رحلة API/OpenAPI/عميل TypeScript موصولة بالمنتج.

## ما أصبح Alpha وما ليس Alpha بعد

| المجال | ما يوجد في الشجرة المفحوصة | الحد الذي يمنع ادعاء اكتماله |
|---|---|---|
| orchestration وRun Gate | خدمة وعقود typed مع فحوص gate واختبارات fake | لا daemon wiring أو runtime فعلي موصول؛ لا merge حقيقي |
| Worktree | عقد ومدير Git بعمليات إنشاء/حذف/حالة صريحة | اختبارات المدير الحالية تستخدم runner مزيفاً؛ لا ربط بتشغيل المهمة |
| Validator | قائمة argv مضبوطة، timeout، حدود مخرجات وrunner | لا sandbox، ولا تحديد worktree كـcwd، ولا تكامل تشغيل موثق |
| ProviderConfig | تهيئة صريحة ومهايئ compat لـClaude/DeepSeek مع حدود اختيار/إخراج | لا أسرار مُدارة أو تحقق بمزود حقيقي أو تكامل daemon |
| API | handler معزول واختبارات محلية لحدود الطلب والاستجابة | غير مسجل في خادم المنتج، ولا OpenAPI/عميل/تدفق إلغاء دائم |
| اختبارات Alpha | اختبار E2E اصطناعي وخمس حزم مستهدفة ناجحة | لا تثبت ذلك تشغيل التطبيق بكامله ولا `go test ./...` |

## الفجوات إلى محطة قبول تقارب 70%

هذه عناصر عمل متبقية، لا خصائص يفترض وجودها:

1. **Daemon wiring:** إنشاء Manager وValidator وProvider Registry وخدمة orchestration من نقطة تركيب daemon، وتمرير dependencies بصورة typed، ودمجهم مع shutdown والإلغاء. إضافة إعداد/feature flag مغلق افتراضياً؛ لا تتجاوز البوابة من UI وحدها.
2. **API مكتملة:** تركيب handler في المسار المقصود فقط بعد مراجعة local/LAN/auth، توثيق مصدر OpenAPI، إعادة توليد TS (`npm run api`)، والتعامل مع التشغيل طويل العمر والحالة والأخطاء والإلغاء. يظل المعالج الموجود نموذجاً معزولاً إلى أن يتم ذلك.
3. **UI:** لا يوجد تدفق مستخدم موصول لهذه الميزة. يلزم عرض الموافقة والبيانات الخارجة والمزود/النموذج والميزانية والحالة ونتيجة `hold`/`merge`، مع إجراءات إلغاء/مراجعة؛ لا تُرسل credentials للـrenderer.
4. **GitHub:** مدير Worktree المحلي لا يغطي تكامل GitHub. يلزم تحديد ما إذا كان النطاق يشمل push/PR/checks/comments، ثم تنفيذ صلاحيات وcredential management وموافقة صريحة واختبارات API مزيفة/تعاقدية قبل اتصال حقيقي.
5. **Sandbox:** القائمة البيضاء وargv بدون shell لا توفر عزلاً للعملية أو الشبكة أو الموارد. يلزم تشغيل العامل والفحوص ضمن جذر عمل محدود وحساب/حاوية/VM وفق مستوى المخاطر، وبيئة بلا أسرار وحدود وقت/موارد/شبكة. اختبارات السياسة ليست برهان sandbox.
6. **Persistence:** `ProjectMemory` واجهة فقط في هذا المسار، وحالة الخدمة ومعالج HTTP مؤقتة في الذاكرة. يلزم تحديد retention والتدقيق، ثم تخزين run/state/outcome مع migrations واختبارات الاستعادة وidempotency/fencing قبل الوعد بالاستئناف بعد إعادة تشغيل daemon.
7. **Real providers:** اختبارات محلية لا تكفي. اختر مزوداً واحداً أولاً، وثبت طريقة API والتوافق والنموذج والمضيف، وخزن الاعتماد في SecretStore، وطبّق allowlist egress، موافقة نقل البيانات، الإلغاء/timeout، limits/redaction والميزانية؛ اختبر باتصال حقيقي opt-in دون أسرار في logs. وسّع للمزود التالي بعد قبول الأول.

## خطة قبول تدريجية وبوابات قرار

1. **بوابة A — Alpha محلي (متحقق جزئياً):** إعادة تشغيل الاختبار المحدد والحزم الخمس في قسم التشغيل؛ فحص نتائج `hold` والرفض وعدم وجود استدعاء سابق للـgate. لا تتطلب شبكة أو مفتاحاً. لا تنتقل إلى قبول منتجي اعتماداً على هذه المرحلة وحدها.
2. **بوابة B — تكامل محلي قابل للتكرار:** أضف اختباراً بمستودع Git مؤقت فعلي لـCreate/Status/Remove، مع تحقق أن إزالة worktree لا تحدث ضمناً. اربط Validator بجذر worktree معروف واختبر فشل/timeout/إلغاء وألا يستخدم argv من الخطة. اقبل فقط إذا لم يمكن لمس مسار خارج الجذر عبر حالات الاختبار المحددة.
3. **بوابة C — تركيب daemon بـfake adapters:** وصّل الخدمة ومكوّنات fake خلف flag مغلق افتراضياً، واختبر أن الإعداد الافتراضي والغياب والأخطاء كلها fail-closed، وأن shutdown والإلغاء لا يتركان أعمالاً يتيمة. لا تستخدم مزوداً خارجياً بعد.
4. **بوابة D — API محلي وعقد مُولّد:** سجّل المسارات بعد مراجعة auth وLAN، وحدث OpenAPI/الأنواع من المصدر. اختبر local مقابل remote/Origin، طلبات غير صالحة، الاستطلاع، الحالة المفقودة، ومنع تسريب prompt/output/key، ثم نفّذ smoke على daemon فعلي.
5. **بوابة E — حالة دائمة وواجهة:** اعتمد مخطط الاستعادة والاحتفاظ والمهاجرة قبل SQLite؛ اختبر restart، duplicate requests، fencing، cancel، وترحيل/استعادة. بعدها فقط اربط UI خلف flag، مع موافقة واضحة وتجربة فشل و`hold` وإخفاء كل الأسرار.
6. **بوابة F — عزلة وتكامل GitHub:** أثبت العزل وحدود الموارد/الشبكة وحماية الملفات في بيئة sandbox مستقلة؛ اختبر سيناريوهات GitHub بصلاحيات ضيقة وfake server أولاً، ثم مراجعة أمنية لمسار credentials قبل أي تفعيل حي.
7. **بوابة G — مزود حقيقي محدود:** بعد مراجعة الخصوصية/التكلفة، فعّل مزوداً واحداً اختيارياً على مستخدم/بيئة تجريبية، مع إيقاف سريع ومراقبة وحدود إنفاق ونجاح/فشل/إلغاء موثق. لا توسع providers ولا تعلن محطة «70%» قبل قبول البوابات المطلوبة أعلاه وتوثيق الأدلة.

## حدود هذا الدليل

التحقق المسجل يقتصر على أوامر Go المذكورة ونتائجها في شجرة العمل وقت إعداد الدليل. لم يُشغّل `go test ./...`، ولم يُتحقق من بناء daemon أو OpenAPI/TS أو UI، ولم تُجرَ مكالمة إلى GitHub أو مزود نماذج حقيقي، ولم تُختبر persistence أو sandbox فعلية. راجع أيضاً [`02-backend-integration.md`](02-backend-integration.md) و[`02-backend-verification.md`](02-backend-verification.md) و[`05-security-notes.md`](05-security-notes.md) مع الانتباه إلى أن الملفات الأولية الحالية تضيف عقوداً ومكونات جديدة بعد بعض أوصاف التكامل السابقة.
