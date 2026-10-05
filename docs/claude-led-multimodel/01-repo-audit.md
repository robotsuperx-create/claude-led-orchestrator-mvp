# Repo Audit — Claude Executive Orchestrator + Model Gateway

**النطاق / Scope:** مراجعة فعلية لبنية `backend/`, `frontend/`, و`packages/` لتحديد دمج تدريجي لا يكسر إعادة الكتابة الحالية. هذا تقرير معماري فقط؛ لم يتغير كود الإنتاج.

## 1. المكونات الحالية / Current components

- **Backend:** خدمة Go طويلة التشغيل؛ حدودها الفعلية هي `domain`, `ports`, `service/*`, `session_manager`, `storage/sqlite`, `httpd`, و`daemon` (موثقة كذلك في `AGENTS.md` و`docs/architecture.md`). واجهة HTTP مبنية بـ chi، و`httpd.APIDeps` يحقن الخدمات في المتحكمات؛ التوجيه/عقود OpenAPI مولّدة من مصدر Go.
- **Agent/Chat:** تكامل Claude موجود بالفعل عبر `backend/internal/adapters/agent/claudecode`, `backend/internal/adapters/chatdriver/claudeacp`, و`backend/internal/adapters/reviewer/claudecode`. `ports/chat.go` يفصل Chat ذي البروتوكول/المحادثة المستمرة عن Agent/Runtime الخاص بتشغيل CLI. `service/chat` يدير المحادثات والاستئناف/الموافقات؛ التسجيل في `adapters/chatdriver/registry`.
- **Model selection (ليس Gateway عامّاً):** `service/agent` و`ports/agent.go` يقدمان اكتشاف/تخزين مؤقت لكتالوج نماذج الوكلاء، بينما Chat يعرض قدرات وخيارات النموذج التي يعلنها مزود الجلسة (واجهات `/conversation/models` و`/conversation/config-options`). هذه واجهات اختيار داخل مزود قائم، لا طبقة توجيه موحدة لطلبات LLM.
- **Reviewer gateway:** `backend/internal/reviewgateway/gateway.go` يجهّز مجلدات/manifest خاصة بالمراجع داخل `AO_DATA_DIR`. هذا عزل وتشغيل مراجع، وليس بوابة استدعاء نماذج؛ لا ينبغي إعادة تسميته أو تحميله مسؤولية الـModel Gateway.
- **Frontend/packages:** Electron + React يستهلك واجهة daemon عبر الأنواع المولدة `frontend/src/api/schema.ts`؛ نقاط الواجهة ذات الصلة `SessionChatSurface`, hooks في `frontend/src/renderer/hooks/useConversation.ts` و`useAgentModelsQuery.ts`. `frontend` يعتمد محلياً على `packages/product-ui`. `packages/shared` يحوي حالياً أدوات مشتركة محدودة (مثل `chat/ansi.ts`)، و`packages/cloud-client` عميل منفصل. المجلد `packages/claude-led-orchestrator/` موجود لكنه **فارغ** في الشجرة المفحوصة (لا manifest أو ملفات متتبعة)، فلا يُعامل كحزمة قائمة.

## 2. نقاط الامتداد الواقعية / Realistic extension points

1. **Model Gateway:** أضف عقداً ضيقاً جديداً في `backend/internal/ports/` وتنفيذاً مزودياً في `backend/internal/adapters/`، ثم خدمة تطبيقية جديدة/محدودة في `backend/internal/service/` وتوصيلها من `backend/internal/daemon`. حافظ على استقلالها عن `ports.ChatDriver`: الـGateway لاستدعاءات النموذج/التوجيه، وChat driver يملك دورة محادثة أصلية طويلة العمر، history، approvals، resume، وcontroller fencing. ابدأ باستدعاءات داخلية فقط؛ لا تغيّر اختيار نموذج المستخدم في جلسات Chat إلا عبر قدرات المزود القائمة.
2. **Executive Orchestrator:** ضعه في خدمة تطبيقية backend (أو وسّع `service/automation` فقط إذا كان السلوك المطلوب فعلاً أتمتة/تشغيلات)، واستعمل واجهات `service/session`/`session_manager` وChat لبدء العمل والتواصل. أبقِ lifecycle والعمليات متعددة الخطوات ضمن حدود الخدمات الحالية؛ لا تجعل UI أو gateway يكتب SQLite أو يشغّل runtime مباشرة. توجد فعلاً مسارات `automations`, `cues`, و`reports` يمكن فحصها قبل إنشاء مفهوم منتج جديد.
3. **API/UI:** أضف controller/DTO وعقد API عبر `backend/internal/httpd/controllers`, `APIDeps`, `httpd/apispec/specgen/build.go` ثم شغّل `npm run api`. اربط UI من React hooks/المكونات واستخدم `frontend/src/api/schema.ts` المولّد، لا نسخ DTO يدوية.
4. **حالة دائمة:** لا تضف جداول/إعدادات قبل حسم متطلبات الاستئناف والتدقيق. إذا لزم حفظ runs أو قرارات، أضف domain/store ومهاجرة SQLite جديدة (لا تعدّل مهاجرات مدمجة)، ومرّر التغييرات عبر قواعد CDC القائمة.

## 3. مخاطر الدمج / Integration risks

- **خلط البروتوكولات:** Claude ACP/CLI الحالي ليس مكافئاً لـAnthropic API أو gateway متعدد المزودين. تبديل مسار الجلسات الأصلية قد يكسر استئناف المحادثة، checkpoints، approvals، الأدوات، أو handoff.
- **الأمان والخصوصية:** إرسال prompts أو محتوى worktree إلى مزود خارجي يخلق egress/احتفاظ/تكلفة ومخاطر أسرار. خزّن الاعتمادات في daemon فقط، ولا تعرضها للrenderer أو السجلات/الأحداث/SQLite كنص صريح؛ حدّد allowlist للمزود والنموذج وtimeouts وحدود الإنفاق والإلغاء.
- **صلاحيات التشغيل:** الوكيل يستطيع التفويض عبر بيئة AO المضبوطة؛ لا تمنح orchestrator صلاحيات workspace أو approval أوسع من الجلسة، ولا تتجاوز حالة `blocked` أو موافقات الأدوات.
- **السطح الشبكي:** المستمع الأساسي موثق كـ`127.0.0.1` وغير موثّق؛ لا تضف منفذاً أو listener جديداً ولا تغيّر قواعد LAN. أي route جديدة يجب أن تراجع اختبارات LAN/auth، خصوصاً إن لم يكن وصول الهاتف مقصوداً.
- **تغييرات العقد/التخزين:** OpenAPI والأنواع مولدة، وSQLite/sqlc/CDC لها مصادر حقيقة واختبارات drift. لا تعدّل الملفات المولّدة مباشرة.
- **حالة الحزمة:** بناء تنفيذ جديد داخل `packages/claude-led-orchestrator` دون إعداد build/test/dependency سيصنع مساراً غير موصول بالـmonorepo؛ ابدأ بقرار موثق إن كانت حزمة JS مطلوبة أصلاً أم أن backend Go هو موضعها الطبيعي.

## 4. خطة مراحل / Phased plan

1. **حدود وعقد (صفر مخاطر):** عرّف حالات الاستخدام، بيانات مسموح خروجها، ميزانية/سياسة موافقة، sync مقابل async، وتعايش النموذج مع Chat الحالي. حدّد هل الـGateway API خارجي أم abstraction داخلي.
2. **بوابة تجريبية backend:** أضف port + adapter واحد خلف config opt-in، timeouts/cancellation، أخطاء typed، redaction، حدود token/cost، وfake provider tests باستخدام `httptest`; لا تغيّر جلسات Chat ولا schema بعد.
3. **Executive run service:** أضف خدمة backend رفيعة تستدعي Gateway وتنسّق AO sessions عبر الواجهات القائمة؛ ابدأ بلا تعقيد durable state. عند الحاجة للاستئناف، أضف run records ومهاجرة مع fencing/idempotency وCDC واختبارات الاستعادة.
4. **عقد API وواجهة خلف feature flag:** أضف DTO/routes/OpenAPI ثم ولّد TS؛ اربط عرض الحالة والإجراءات من renderer فقط. لا تعرض secrets، وأضف تحققاً من سياسة LAN.
5. **إطلاق مضبوط:** اختبارات وحدات/عقود وتكامل ثم smoke محلي؛ فعّل لمستخدمين/مزود واحد مع تعطيل فوري، راقب الأخطاء والإنفاق، وبعدها وسّع المزودين.

## 5. أوامر التحقق / Verification commands

```bash
# فحوص backend المستهدفة ثم كاملة
cd backend && go test ./internal/service/chat ./internal/httpd/... ./internal/daemon ./internal/reviewgateway
cd backend && go test ./... && go vet ./...

# عند تغيير API: إعادة توليد OpenAPI وTS من المصدر
cd /home/ubuntu/agent-orchestrator-product && npm run api
cd backend && go test ./internal/httpd/...
npm run frontend:typecheck

# اختبارات الواجهة والبناء الكامل بحسب package scripts
npm --prefix frontend test
npm run lint
```

**ملاحظة تحقق / Verification note:** جرى فحص الملفات والمراجع المذكورة فعلياً؛ الأوامر أعلاه هي بوابة تحقق مقترحة للمرحلة التنفيذية ولم تُشغّل ضمن هذا التدقيق.
