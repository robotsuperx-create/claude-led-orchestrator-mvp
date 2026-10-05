# كتالوج مهارات وأدوات DevOps مفتوحة المصدر

أفضل الخيارات لمنصة Agent Orchestrator هي مزيج من مهارات قابلة للنقل بين الوكلاء، ووصفات تشغيل، وأداة للحلقة المحلية. الترتيب أدناه يوازن بين صلة المشروع بالمجالات المطلوبة، وضوح الرخصة، قابلية انتقاء وحدات صغيرة، ونشاط المستودع؛ عدد النجوم مؤشر انتشار فقط وليس مقياس جودة. أرقام GitHub وتواريخ آخر دفع تعكس لقطة API في 5 أكتوبر 2026.

## أفضل خمسة

1. **[docker/skills](https://github.com/docker/skills) — مرجع Docker الأساسي.** مستودع رسمي من Docker بمهارات `SKILL.md` تشمل بناء Dockerfiles وتحسين الصور، أنماط Compose، Docker Sandboxes، وتهيئة وتشغيل ونشر وكلاء Docker Agent. المهارات تتبع صيغة Agent Skills وتذكر أوامر الحماية من العمليات التدميرية، وتستهدف عدة وكلاء مثل Claude Code وCodex وCopilot وCursor. الرخصة Apache-2.0، والمستودع كان غير مؤرشف ودُفع إليه في 4 أكتوبر 2026؛ أظهر نحو 523 نجمة عند الفحص. هذه أفضل نقطة بدء لمهارات الحاويات لأنها مصدر أولي ومجزأ ويمكن تثبيت نسخة مراجعة بدلاً من متابعة `main` مباشرة. بعض المهارات معلّمة تجريبية، لذا لا تدمجها دون مراجعة توافقها مع إصدارات Docker المستخدمة محليًا. [1] [2]

2. **[github/awesome-copilot](https://github.com/github/awesome-copilot) — أكبر مكتبة انتقاء لوصفات الوكلاء والمهارات.** يجمع تعليمات موجهة للملفات، وكلاء مخصصين، مهارات، إضافات، وخطافات ووصفات عمل لـ GitHub Copilot. يعرّف المستودع المهارات بأنها مجلدات مستقلة قد تتضمن `SKILL.md` وسكربتات وأصولًا، ويسمح بتثبيت المهارة منفردة أو نسخها يدويًا. رخصة المستودع MIT؛ وفي اللقطة كانت لديه نحو 39.7 ألف نجمة، وكان نشطًا حتى 1 أكتوبر 2026. يفيد في اكتشاف وصفات CI، والإصدارات، والتحقيق في الأعطال بدل بناء مكتبة AO من الصفر. لكنه تجميعة يساهم فيها المجتمع، والمشروع نفسه ينبه إلى أن المحتوى مصدره أطراف ثالثة؛ افحص رخصة الملفات ومحتواها وسكربتاتها كلٌّ على حدة، ولا تستورد المكتبة كاملة أو تفترض أن كل وصفة مدققة أمنيًا. [3] [4]

3. **[dash0hq/agent-skills](https://github.com/dash0hq/agent-skills) — أفضل إضافة مركزة للرصد والتصحيح.** يوفر مهارات OpenTelemetry محايدة تجاه مخزن OTLP: إعداد SDK بلغات متعددة، اصطلاحات السمات، إعداد Collector ونشره عبر Docker Compose أو Kubernetes، وتصحيح قواعد OTTL ومسارات البيانات. يدعم أيضًا مراجعة جودة القياس الآلي ضمن CI. الرخصة Apache-2.0، وكان المستودع نشطًا حتى 1 أكتوبر 2026، بنحو 96 نجمة و165 commit كما تعرض صفحة المستودع عند الفحص. رغم حياد صيغة القياسات، المستودع تصونه شركة Dash0 ويتضمن معلومات تكامل خاصة بها؛ خذ إرشادات OpenTelemetry العامة فقط ما لم يختر AO منتجًا بعينه. [5] [6]

4. **[BagelHole/DevOps-Security-Agent-Skills](https://github.com/BagelHole/DevOps-Security-Agent-Skills) — أوسع حزمة تشغيلية للاقتباس الانتقائي.** يغطي تصنيف المستودع CI/CD وDocker وCompose وKubernetes وTerraform والسحابة والرصد والأمن والاستجابة للحوادث، مع قوالب وسكربتات إلى جانب تعليمات المهارات. رخصته MIT، وأظهر نحو 1,133 نجمة و161 fork. توجد إشارتا مراجعة مهمتان: آخر `pushed_at` في GitHub API كان 22 مايو 2026، رغم أن بيانات التحديث كانت أحدث؛ كما أن README يعلن «160+» مهارة في المقدمة بينما الوصف والفهرس يتحدثان عن «80+». لذلك هو مصدر جيد للعثور على قائمة مهارات أولية، لا حزمة نوصي باستيرادها كما هي. تحقق من مسار كل مهارة وسكربت ومن حداثة الإصدارات التقنية قبل النسخ. [7] [8]

5. **[tilt-dev/tilt](https://github.com/tilt-dev/tilt) — حلقة تطوير ومعاينة محلية للخدمات على Kubernetes.** Tilt أداة وليست Skill: يعرّف فريق التطوير بيئته ككود عبر `Tiltfile`، ثم تراقب الأداة تغييرات الملفات وتبني صور الحاويات وتحدّث الخدمات، ما يجعلها مفيدة للتشغيل المتكرر وفحص التطبيق محليًا. رخصتها Apache-2.0؛ عمر المستودع طويل (منذ 2018)، غير مؤرشف، وكان نشطًا حتى 3 أكتوبر 2026، مع نحو 10.1 آلاف نجمة و5,109 commits. أدرجها عندما يحتاج مشروع المستخدم فعلًا إلى خدمات Kubernetes متعددة؛ لا تضف Kubernetes أو Tilt إلى كل مهمة لمجرد وجود معاينة محلية. [9] [10]

## ما يصلح دمجه في Agent Orchestrator

المنتج يملك بالفعل حلقة الإشراف الأساسية: لكل عامل مساحة عمل معزولة، ومتابعة CI ومراجعات PR، ومتصفح للمعاينة؛ كما توصي تعليمات المستودع باستخدام `ao preview` لمعاينة واجهة العامل، وتوجد مهارتا `bug-triage` و`ao-desktop-dev` لتجميع الأدلة وتشغيل تطبيق Electron المحلي بأمان. لذلك الأنسب هو **إضافة معرفة منتقاة إلى منظومة المهارات القائمة، لا استبدالها بخادم DevOps جديد**. [11] [12] [16]

- **ابدأ بمجموعة مهارات داخلية صغيرة** تحت `.agents/skills/`، وهو مسار مستخدم بالفعل في المستودع. استورد أو أعد صياغة `docker-compose-patterns` و`docker-build-strategies` من Docker؛ ثم أضف مهارة مستقلة لـ CI/debug، وأخرى اختيارية لـ OpenTelemetry عند وجود حاجة فعلية. احتفظ بمصدر كل مهارة ورابط commit أو tag ورخصتها في ملف تعريف/ترويسة واضحة. لا تنسخ مكتبات واسعة إلى `backend/internal/skillassets/using-ao/`؛ هذا المسار لمهارات استخدام CLI المضمّنة، وليس مستودعًا عامًا لمعارف البنية التحتية. [1] [5] [12] [16]
- **اجعل الإرشاد يوجّه إلى أدوات AO الموجودة.** عند تعديل تطبيق عامل، وجّه الوكيل إلى `ao preview` وافحص النتيجة في متصفح الجلسة. لا تطلب من Tilt تشغيل معاينة ويب موازية إلا عندما تكون خدمات المشروع على Kubernetes. وعند فشل CI، استخدم حقائق تشغيل CI وسجلاته المرتبطة بالـ worker لإعادة إنتاج الاختبار محليًا ثم أعد الفشل والنتيجة إلى العامل المالك؛ لا تشغّل نشرًا أو تعديلًا إنتاجيًا تلقائيًا.
- **افصل الوصفة عن التنفيذ.** تسمح Skill بتوجيه العامل إلى أوامر Docker أو Kubernetes، لكن تحميلها لا يعني الإذن بتنفيذها. اشترط موافقة صريحة قبل حذف موارد أو صور/حاويات، أو `kubectl apply/delete`، أو نشر خارجي. راجع أي سكربت upstream قبل تشغيله، وامنع تضمين الرموز السرية في سجلات CI أو التعليقات التي يراها العامل.
- **ثبّت المصدر واختبره.** انتقِ مهارات قليلة من مصادر MIT/Apache-2.0، وثبّتها على commit أو إصدار محدد، واحتفظ بإشعارات الرخصة والإسناد. أضف إلى CI تحققًا من سلامة frontmatter، ووجود رابط/رقم إصدار المصدر والرخصة، ومنع سكربتات غير مراجعة من التنفيذ التلقائي. تعليمات المهارات في AO موصوفة كجزء من طبقة العقود للـ agents والـ CI؛ يجب مراجعتها مع الاختبارات وتحديث خريطة التوثيق عند إدخال مسارات مهارات جديدة. [12]

### تنبيه حول Local CI في الإرشادات الحالية

يشير `AGENTS.md` الحالي إلى `npx @redwoodjs/agent-ci run --all` للتشغيل المحلي. المستودع المقابل أعاد تسمية المشروع إلى **Local CI**، وREADME يوصي حاليًا بـ `npx run-local-ci` مع استمرار دعم حزمة التوافق القديمة في إصدارات 0.x. يصف المشروع تشغيل GitHub Actions محليًا مع إيقاف الخطوة عند الفشل وإعادة محاولتها، ما يجعله مناسبًا للتحقيق في CI؛ لكن رخصته **Functional Source License 1.1, MIT Future License (FSL-1.1-MIT)** وليست MIT الحالية: تمنع «الاستخدام المنافس» إلى أن تصبح رخصة MIT المستقبلية نافذة بعد عامين من إتاحة النسخة. لهذا لم أدرجه ضمن الخمسة المفتوحة المصدر، ولا أوصي بتضمينه أو إعادة توزيعه قبل مراجعة قانونية لمدى انطباق القيود على AO. راجع كذلك صلاحية اسم الحزمة القديم في تعليمات المشروع قبل اعتماد هذا المسار. [13] [14] [15] [16]

## المراجع

[1]: https://github.com/docker/skills/blob/main/README.md "Docker Skills README"
[2]: https://api.github.com/repos/docker/skills "Docker Skills GitHub repository metadata"
[3]: https://github.com/github/awesome-copilot "GitHub Awesome Copilot README"
[4]: https://api.github.com/repos/github/awesome-copilot "Awesome Copilot GitHub repository metadata"
[5]: https://github.com/dash0hq/agent-skills "Dash0 OpenTelemetry Agent Skills README"
[6]: https://api.github.com/repos/dash0hq/agent-skills "Dash0 skills GitHub repository metadata"
[7]: https://github.com/BagelHole/DevOps-Security-Agent-Skills "BagelHole DevOps Security Agent Skills README"
[8]: https://api.github.com/repos/BagelHole/DevOps-Security-Agent-Skills "BagelHole skills GitHub repository metadata"
[9]: https://github.com/tilt-dev/tilt "Tilt README"
[10]: https://api.github.com/repos/tilt-dev/tilt "Tilt GitHub repository metadata"
[11]: https://github.com/Untrivial-ai/agent-orchestrator/blob/main/README.md "Agent Orchestrator product workflow and preview"
[12]: https://github.com/Untrivial-ai/agent-orchestrator/blob/main/docs/documentation-map.md "Agent Orchestrator documentation and skill contract map"
[13]: https://github.com/redwoodjs/local-ci/blob/main/packages/cli/README.md "Local CI CLI README"
[14]: https://github.com/redwoodjs/local-ci/blob/main/LICENSE "Local CI Functional Source License"
[15]: https://api.github.com/repos/redwoodjs/local-ci "Local CI GitHub repository metadata"
[16]: https://github.com/Untrivial-ai/agent-orchestrator/blob/main/AGENTS.md "Agent Orchestrator operating instructions"
