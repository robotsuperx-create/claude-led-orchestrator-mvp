# عرض محلي كامل لدورة Claude-led Multi-model

## تشغيل اختبار الدخان

المتطلب: Go بإصدار متوافق مع `backend/go.mod`. من جذر المستودع شغّل:

```bash
cd backend
go test ./internal/integration -run '^TestClaudeOrchestratorLocalDemoSmoke$' -count=1 -v
```

`-count=1` يعطّل إعادة استخدام نتيجة الاختبار المخزنة، و`-v` يعرض سيناريوهَي القبول وطلب التعديلات. يمكن إعادة الأمر نفسه للتحقق مرة أخرى. الاختبار لا يحتاج daemon شغالاً، أو مفتاح مزود، أو Docker، أو GitHub.

## ما الذي يشغّله الاختبار؟

`backend/internal/integration/claude_orchestrator_local_demo_test.go::TestClaudeOrchestratorLocalDemoSmoke` يبني الـrouter الفعلي مع تفعيل flag داخل إعداد الاختبار وحقن خدمة orchestration الفعلية (`claudeorchestrator.Service`). يرسل طلب بدء opt-in إلى المسار الداخلي عبر `httptest` ثم يستطلع مسار الحالة حتى تنتهي الدورة. كل الاعتمادات الخارجية fakes محلية:

1. يقرأ `ProjectMemory` سياقاً محلياً وهمياً.
2. يعيد مخطط Claude fake خطةً تتضمن عاملاً محدداً بـ`deepseek` ومسار worktree التجريبي.
3. يفحص DeepSeek worker fake أن المزود typed هو `deepseek`، ويستعلم من Worktree manager fake عن المسار، ثم يكتب ملفاً صغيراً داخل مجلد مؤقت أنشأه الاختبار.
4. يتحقق Validator fake من نتيجة العامل والملف المكتوب.
5. يعيد Claude reviewer fake موافقة أو يطلب تعديلات.
6. يسجل ProjectMemory fake النتيجة، ويُختبر status عبر HTTP.

يشغّل الاختبار حالتين: توصية `merge` تصل إلى الحالة `completed`، وحالة `hold` تصل إلى `held`. كما يثبت ترتيب الاستدعاءات، استخدام Claude للتخطيط والمراجعة وDeepSeek للعامل، استدعاء validator وWorktree status، وعدم إنشاء worktree أو إزالته تلقائياً. ردود API لا تحتوي إلا `runId` و`state`.

## حدود مهمة

- الطلبات تمر إلى خادم `httptest` محلي داخل عملية الاختبار فقط؛ لا توجد اتصالات بمزودي Claude أو DeepSeek أو الإنترنت، ولا تُقرأ أسرار أو متغيرات اعتماد.
- مجلد العامل المؤقت مع Worktree manager fake **ليس مستودع Git حقيقياً**. لا ينفذ الاختبار أوامر Git أو ينشئ فرعاً/commit/worktree حقيقياً.
- `merge` توصية من orchestrator وليست عملية دمج فعلية. لا ينفذ الاختبار `git merge` ولا يكتب إلى GitHub.
- يحقن الاختبار الخدمة مباشرة في router محلياً. هذا ليس daemon end-to-end ولا يعني أن daemon الإنتاجي يركب الخدمة؛ مسارات التحكم تجريبية، داخلية، ومحمية بالـfeature flag واشتراط الموافقة الصريحة. لا يفعّل الاختبار إعداداً أو route في تشغيل المنتج.
- الحالة ونتيجة ProjectMemory الوهميتان مؤقتتان وتعيشان فقط أثناء الاختبار.
