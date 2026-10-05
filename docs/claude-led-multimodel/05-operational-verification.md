# التحقق التشغيلي: Claude-led Multi-model

## الحالة المختصرة

اجتازت اختبارات الحزم المتأثرة واختبار HTTP smoke المحلي. النتيجة **ليست** أن orchestration أصبح ميزة تشغيلية في daemon أو منتجاً متصلاً بمزود: اختبار smoke يستخدم `httptest` وGate وخدمة مزيفين، ولا يستدعي مزوداً حقيقياً أو GitHub أو Docker أو تخزين تشغيلات SQLite.

يظل `AO_CLAUDE_ORCHESTRATOR_FEATURE_ENABLED` **مغلقاً افتراضياً**. الراوتر يستطيع تسجيل المسار الداخلي فقط عندما يحقن المستدعي `APIDeps.ClaudeOrchestrator`؛ تركيب daemon الحالي لا يحقن هذه التبعية، لذا يبقى المسار غير مسجل في daemon الفعلي. لا توجد إضافة لمسار عام تحت `/api/v1` أو تغيير في OpenAPI، ولم تُضف بيانات اعتماد شبكية.

## ما يثبته HTTP smoke فعلياً

الاختبار `backend/internal/integration/claude_orchestrator_smoke_test.go::TestClaudeOrchestratorHTTPIntegrationSmoke` ينشئ `httptest.NewServer` ويسجل `ClaudeOrchestratorAPI` مباشرةً مع Gate وخدمة fake. يثبت ضمن هذا harness المحلي أن:

- `POST /internal/claude-orchestrator/runs` مع موافقة صريحة يقبل الطلب بـ`202`، ويعيد `runId` و`pending`، ثم يستدعي الخدمة fake بالـtask و`maxRetries` المتوقّعين.
- `GET /internal/claude-orchestrator/runs/{runId}` يستطلع الحالة النهائية `completed` التي تعيدها الخدمة fake.
- طلباً يحمل `Host` غير محلياً وطلباً يفتقد `explicitOptIn` يُرفضان.
- الاستجابات لا تتضمن إلا `runId` و`state` ولا تكشف task أو ناتج التخطيط/العامل أو نص خطأ المزود الذي زرعه الاختبار كقيم سرية. هذا اختبار إسقاط/redaction لعقد الاستجابة، وليس ضماناً لتخزين دائم أو redaction شامل لكل مسارات السجلات.

تغطي `backend/internal/httpd/claude_orchestrator_router_test.go` كذلك غياب المسار عند غياب التبعية، والرفض مع الإعداد الافتراضي للـflag، والـOrigin/المتصل غير المحلي، واشتراط opt-in، وبقاء المسار خارج نطاق `/api/v1`. أما حظر مسارات `/internal/` على مستمع LAN فيطبقه `lanControlBlock` مستقلاً عن `Host` المرسل. اختبارات الراوتر وsmoke لا تشغل daemon حقيقياً؛ اتصال HTTP فيها إلى خادم اختبار محلي فقط.

## حدّ الخلفية والعقد

- `APIDeps.ClaudeOrchestrator` هو واجهة خدمة typed اختيارية. `mountClaudeOrchestrator` يبني سياسة التشغيل من `cfg.ClaudeOrchestrator.FeatureEnabled`؛ لا يكفي وجود التبعية، إذ يبقى flag مغلقاً افتراضياً ويلزم `explicitOptIn: true` لكل بدء. كما ترفض handlers الطلبات ذات `Origin` أو `Host` غير loopback. إعداد flag يدوياً لا يفعّل الميزة افتراضياً.
- مسارات العقد الداخلي هي `POST /internal/claude-orchestrator/runs` و`GET /internal/claude-orchestrator/runs/{runId}`؛ استجابة البدء والحالة `{runId,state}`. المسار غير جزء من OpenAPI المولّد، وDTO المقترح في `controllers/claude_orchestrator_contract.go` ليس عقد هذه handlers. daemon لا يمرر `ClaudeOrchestrator` ضمن `APIDeps` حالياً، لذلك لا تعرض عملية daemon واجهة بدء/حالة عاملة.
- الحالة التي يديرها handler في الذاكرة فقط؛ لا توجد استعادة بعد restart ولا endpoint إلغاء. تعد `held` حالة نهائية في الخدمة، وقد عُدّل إسقاط واجهة product-ui كي يوقف polling ولا يعرض cancel لها.
- `WorkerRuntime` الجديد يمكن، عند تركيبه صراحةً، اختيار worktree موجود من metadata وتشغيل argv ثابتة مهيأة من إعداد موثوق دون shell، ثم استدعاء `ports.Validator`. لا ينشئ أو يزيل worktrees ولا يدمج فروعاً. هذا التنفيذ غير موصول في daemon، واختبار smoke لا يشغله ولا يختبر Git حقيقياً.
- عقد عميل SCM ومهايئه provider-neutral، واختباراته تستخدم خادماً HTTP محلياً مزيفاً. ذلك لا يعادل تكامل GitHub أو ربط credential حقيقي أو إجراء push/PR/check على مستودع خارجي.

## واجهة TypeScript

- نموذج `@aoagents/product-ui` يقبل إسقاطاً محدوداً من `{runId,state}`، ولا يمرر خصائص زائدة مثل مفاتيح الاعتماد إلى نموذج العرض. يطابق `held` الحالة النهائية التي تعيدها الخدمة.
- hook renderer في `frontend/src/renderer/hooks/useOrchestratorRun.ts` يعتمد `OrchestratorRunClient` محقوناً، مع `enabled` وconsent مغلقين افتراضياً، ويكوّن body من حقول task المسموحة فقط. لا يوجد implementation موصول لهذا العميل ولا mount للواجهة في الشاشة الافتراضية؛ اختبارات product-ui تختبر النموذج/المكوّن المنفصل لا رحلة daemon كاملة.
- واجهة العميل لديها `cancelRun` ونوع wire يتضمن `cancelled` كحد مستقبلي، لكن daemon الحالي لا يقدم cancel route ولا حالة cancel في عقده. لذلك يجب أن يبقى `cancelEnabled` غير مفعل؛ وجود interface أو fake test لا يعني أن الإلغاء يعمل في المنتج. لم تُولّد مسارات OpenAPI أو تضف إلى schema.
- لم يُنفذ typecheck/test شامل لحزمة `frontend` في هذا التحقق؛ `frontend/node_modules` غير موجودة في بيئة الفحص. نجاح `packages/product-ui` لا يثبت بناء renderer الكامل.

## ما لم يُنفّذ أو لم يصبح حقيقياً

| المجال | ما توفره الشجرة/الاختبارات | ما لم يحدث في هذا التحقق |
|---|---|---|
| مزود النموذج | عقود وعميل/إعدادات موجودة، واختبارات محلية | لا طلب إلى Claude أو DeepSeek أو أي مزود حقيقي، لا credential حقيقي، ولا تحقق من تشغيل end-to-end عبر مزود. service في daemon بلا adapters. |
| GitHub / SCM | عميل HTTP typed عام مع حدود للمهلة/الرد والموافقة على الكتابات؛ اختبارات `httptest` | لا اتصال بخدمة GitHub، ولا PAT أو اعتماد آخر، ولا branch/push/PR/check حقيقي. لا يُعد adapter العام تكاملاً خاصاً بـGitHub. |
| Git وworktree | WorkerRuntime يقبل worktree موجوداً وargv ثابتة؛ اختبارات بمنافذ/runner fake | smoke لا ينفذ أوامر Git أو تغييرات مستودع؛ لا ربط runtime بالـdaemon، ولا merge أو remove worktree ضمن هذا المسار. |
| Docker / sandbox | `BuildDockerCommand` ينشئ argv مقيدة، وطبقة الخدمة تقبل runner محقوناً | لا تستدعي الشيفرة `docker run` ولا يوجد runtime Docker موصول أو عزل مُختبر. بناء argv ليس sandboxاً. |
| SQLite / استمرارية التشغيل | مخزن `memory` للاختبار والعقود؛ run map مؤقت في API | لا migration أو store SQLite خاص بتشغيلات orchestration، ولا حفظ/recovery/idempotency دائم موصول أو منفذ. قاعدة SQLite القائمة تخص بيانات المنتج الأخرى ولا تثبت استمرارية هذه التشغيلات. |
| daemon/UI | route اختيارية بالحقن، وhook/model منفصلان | daemon لا يحقن خدمة orchestration في `APIDeps`، وroute لا تكون مركبة فيه، والواجهة ليست موصولة. لا توجد رحلة مستخدم تشغيلية. |

## أوامر التحقق المنفذة

من جذر المستودع، نُفذ `/usr/local/go/bin/gofmt -w` على ملفات Go الجديدة والمتأثرة. ثم من `backend/`:

```bash
/usr/local/go/bin/go test ./internal/service/claudeorchestrator ./internal/ports ./internal/httpd ./internal/httpd/controllers ./internal/adapters/scm ./internal/integration
```

**النتيجة:** اجتازت الحزم الست كلها.

ومن جذر المستودع:

```bash
npm --prefix packages/product-ui run typecheck \
  && npm --prefix packages/product-ui test \
  && npm --prefix packages/product-ui run build
npm --prefix packages/claude-led-orchestrator run check
```

**النتيجة:** product-ui نجح في typecheck و140 اختباراً وbuild؛ و`claude-led-orchestrator run check` نجح في typecheck و28 اختباراً وbuild. أُعيدت اختبارات product-ui بعد إصلاح حالة `held` النهائية، ونجحت. لم يُشغّل `go test ./...` أو build كامل للـrenderer.

## قرار التشغيل

هذه النتيجة تثبت **عقداً محلياً قابلاً للاختبار ومسار HTTP fake** فقط. أبقِ feature flag على حاله (مغلقاً افتراضياً) ولا تعتبر المسار مفعلاً لمستخدم المنتج قبل توفير adapters حقيقية ومراجعة egress/الأسرار، runtime sandbox، تخزين دائم، lifecycle للإلغاء، تركيب daemon/عميل UI، واختبارات تكامل منفصلة لهذه الحدود. لا توجد في هذا التغيير بيانات اعتماد أو routes عامة جديدة.
