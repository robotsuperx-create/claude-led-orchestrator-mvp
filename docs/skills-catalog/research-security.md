# كتالوج بحث: مهارات وقواعد وكلاء أمن البرمجيات

**تاريخ الفحص:** 2026-10-05  
**النطاق:** مهارات وقواعد مفتوحة منشورة لمراجعة أمن البرمجيات تحت Claude، مع فحص المستودعات والرخص والمراجع المعلنة. هذا تقييم ملاءمة وشفافية للمصادر، وليس اختبارًا تجريبيًا لمعدل اكتشاف الثغرات. لا أوصي بمهارات اختبار اختراق أو استغلال هجومي؛ التركيز مراجعة الكود، النمذجة الدفاعية، الأسرار، التبعيات ومعايير OWASP.

## الخلاصة التنفيذية

أفضل نقطة بداية متعددة الأغراض هي [OWASP Secure Agent Playbook](https://github.com/OWASP/secure-agent-playbook)، مع انتقاء مهارات مراجعة الكود والأسرار وSCA فقط. لتعميق مراجعة التغييرات ومخاطر سلسلة التوريد، أضف مهارات محددة من [Trail of Bits](https://github.com/trailofbits/skills). لا تعتمد على أي مهارة لغوية بوصفها حاجزًا وحيدًا: شغّل ماسح أسرار وماسح ثغرات تبعيات حتميَّين في CI، ثم اجعل مخرجات Claude مراجعةً موثقةً تتطلب قبولًا بشريًا.

درجات الثقة أدناه **تقدير تحريري لمدى ملاءمة المورد ووضوح أدلته**، لا قياسًا لدقة النموذج ولا تصنيفًا أمنيًا رسميًا. النجوم وعدد الالتزامات مؤشرات نشاط/انتشار فقط، وليست دليل جودة أو ضمان أمان.

## أفضل خمسة موارد

| الترتيب | المورد | أفضل استخدام وتغطية | الرخصة التي يعلنها المستودع | الثقة |
|---|---|---|---|---:|
| 1 | [OWASP Secure Agent Playbook](https://github.com/OWASP/secure-agent-playbook) | أوسع حزمة موحّدة: `code-review-security` و`secrets-scan` و`sca-audit` وAPI/Web/IaC، إضافة إلى `multi-agentic-threat-model`. إجراءات موثّقة ومخرجات تربط النتائج بـCWE/ASVS ومراجع؛ مناسبة كإطار أساسي إذا انتُقيت المهارات الدفاعية. | CC BY 4.0 في [LICENSE](https://github.com/OWASP/secure-agent-playbook/blob/main/LICENSE.md). | **0.88** |
| 2 | [Trail of Bits Skills](https://github.com/trailofbits/skills) | أقوى حزمة متخصصة للتدقيق: مراجعة تفاضلية للتغييرات، فهم سياق الكود، تحقق من الإيجابيات الكاذبة، تحليل أنماط الثغرات، وأداة/مهارة تدقيق مخاطر التبعيات. الأخيرة تجمع بيانات بشكل حتمي ثم تفصل القياس عن تفسير النموذج. | CC BY-SA 4.0 في [LICENSE](https://github.com/trailofbits/skills/blob/main/LICENSE). | **0.92** |
| 3 | [UnitOneAI/SecuritySkills](https://github.com/UnitOneAI/SecuritySkills) | حزمة واسعة قابلة للاستخدام مع Claude وغيرها: مهارات مستقلة لـ`threat-modeling` و`secure-code-review` و`dependency-scanning` و`owasp-top-10-web`، وخرائط معلنة إلى OWASP/NIST/CIS. مفيدة لاختيار خطوات حسب الدور بدل تحميل حزمة ضخمة. | MIT في [LICENSE](https://github.com/UnitOneAI/SecuritySkills/blob/main/LICENSE). | **0.77** |
| 4 | [agamm/claude-code-owasp](https://github.com/agamm/claude-code-owasp) | قاعدة مركزة لـClaude Code: سير مراجعة، تحقق من مسار المدخل إلى المصب، قواعد تصنيف النتائج، مراجع لغات، وملفات OWASP ASVS وTop 10. وجود حالات eval تشمل كودًا ضعيفًا ونظيرًا آمنًا ميزة عملية لمراجعة تغييرات المهارة. | MIT في [LICENSE](https://github.com/agamm/claude-code-owasp/blob/main/LICENSE). | **0.85** |
| 5 | [Anthropic Claude Code Security Review](https://github.com/anthropics/claude-code-security-review) | تكامل Claude مباشر لمراجعة فرق PR ونشر النتائج على السطور؛ مفيد كطبقة ثانية لمراجعة تغييرات التطبيق، لا كبديل عن SAST/SCA أو النمذجة. | MIT في [LICENSE](https://github.com/anthropics/claude-code-security-review/blob/main/LICENSE). | **0.87** |

### مبررات وحدود الاختيار

1. **OWASP Secure Agent Playbook — 0.88.** يعلن المشروع 18 مهارة، وإجراءات قابلة للاستخدام مع Claude Code أو كقوائم عمل مستقلة. شجرة [code-security-skills](https://github.com/OWASP/secure-agent-playbook/tree/main/plugins/code-security-skills/skills) تتضمن صراحة مراجعة الكود وSCA والأسرار. تشمل النمذجة المنشورة تحديدًا أنظمة الوكلاء متعددة الوكلاء عبر CSA MAESTRO؛ لا ينبغي افتراض أنها بديل كامل لنمذجة تهديدات تطبيق ويب/خدمة تقليدية. المشروع حديث نسبيًا؛ كما يشير README الخاص بمراجعة الويب إلى OWASP Top 10 (2021)، بينما صفحة OWASP الرسمية تصف **Top 10:2025** بالإصدار الحالي المنشور. ثبّت الإصدار، وتحقق من خرائط المعايير قبل كل اعتماد.
2. **Trail of Bits — 0.92.** مستودع متخصص ذو نشاط وانتشار ظاهرين، ويصف المصدر إجراءات للتدقيق ومهارات تحقق من النتائج. يوضح [Supply Chain Risk Auditor](https://github.com/trailofbits/skills/tree/main/plugins/supply-chain-risk-auditor) أنه لا يثبت أو يبني الحزم، ويصرح بما تعذر تقييمه بدل اعتباره سليمًا. حدوده مهمة: تغطية قواعد/ملفات قفل بعينها، منها أنه لا يقرأ `yarn.lock` أو`pnpm-lock.yaml` أو`poetry.lock` بحسب README، لذا يجب عدم مساواة غياب نتيجة بفحص كامل. توجد قائمة [Trail of Bits curated](https://github.com/trailofbits/skills-curated) التي تقول إن الإضافات المنشورة خضعت لمراجعة موظفي Trail of Bits، لكنها سوق منفصلة وليست شهادة تدقيق تلقائية لكل إصدار مستقبلي.
3. **UnitOneAI — 0.77.** نقطة قوة واضحة هي فصل المهارات وهيكلها مع خرائط إطار معلنة؛ المستودع نفسه يحذر من احتمال أخطاء/تقادم المراجع ويوصي بالتحقق من معرفات الضوابط والمعالجات. مشروع حديث مقارنةً بالأدوات الراسخة، لذلك اعتبر ادعاءات «مقاوم لحقن التعليمات» والتغطية الذاتية ادعاءات من المشروع حتى تُراجع الملفات ونتائج الاختبارات في إصدار مثبت.
4. **agamm — 0.85.** README يحدد مصادر تحقق مثل OWASP Top 10:2025 وASVS 5.0 ويشرح ملفات المراجع وحالات eval. هذا يجعلها قاعدة مراجعة جيدة لقواعد Claude، لكن محتواها الإرشادي لا يمسح تاريخ Git للأسرار ولا يحل محل فاحص CVE آلي. تحقق من تحديث المراجع؛ تكرار ذكر إصدار حديث في README ليس بحد ذاته إثباتًا لصحة كل بند.
5. **Anthropic — 0.87.** توثيق المستودع يذكر مراجعة فرق التغييرات وتحليلات مثل حقن SQL/XSS والمصادقة والأسرار والتبعيات، لكن أهم قيد صريح: الإجراء **غير محصّن ضد prompt injection**، ويُوصى بتشغيله فقط على PRs موثوقة أو بعد موافقة المراجع على مساهم خارجي. لا تمنح تشغيل PR غير موثوق سر API أو صلاحية كتابة؛ ثبّت action إلى SHA ثابت، وقلّص أذونات GitHub Actions. النتائج/التعليقات ليست بذاتها شرط منع دمج، فاجعل ضوابط CI مستقلة إلزامية.

## حواجز مراجعة مقترحة تحت Claude

طبّق الحواجز على مراحل، واجعل الماسحات التقليدية هي التي تحدد الفشل الآلي. Claude يساعد على تفسير السياق وإنتاج دليل؛ لا تمنحه سلطة دمج أو تغيير سياسة الاستثناءات.

1. **قبل التصميم أو عند تغيير حدود الثقة:** شغّل `threat-modeling` من SecuritySkills للأنظمة العامة، أو إجراء OWASP للأنظمة متعددة الوكلاء عند ملاءمته. اطلب مخطط أصول/مداخل/حدود ثقة/مهاجمين/مسارات إساءة استخدام/تدابير، واربط كل خطر بقرار تصميم ومالك. بوابة الدمج: لا يُقبل تغيير أمني جوهري بلا تحديث النموذج أو تبرير موثق لعدم انطباقه.
2. **كل طلب دمج:** استخدم `code-review-security` من Playbook و/أو مراجعة diff من Trail of Bits أو Anthropic. لتقليل الضجيج، لا تشغّل الحزم الثلاث كأنها ثلاثة أحكام مستقلة؛ اختر مراجعة أساسية واحدة ومراجعة متخصصة عند مخاطر التغيير. اشترط لكل نتيجة ملفًا/سطرًا، مسار مدخل غير موثوق إلى sink، أثرًا قابلًا للتفسير، شدة، مرجع CWE/ASVS عند انطباقه، إصلاحًا محددًا وثقة. ارفض النتائج العامة بلا دليل، واطلب من Claude ذكر نطاق ما لم يراجعه.
3. **منع تسريب الأسرار:** أضف [Gitleaks](https://github.com/gitleaks/gitleaks) (MIT) كفحص محلي/CI للملفات وتاريخ Git، مع تقارير منقّحة وعتبة تمنع الأسرار الجديدة؛ عالج أي كشف بتدوير السر لا بحذفه من آخر commit فقط. لاحظ أن README الحالي يصف المشروع بأنه مكتمل الميزات وأن التطوير المستقبلي يقتصر على تصحيحات أمنية؛ ثبّت نسخة، راقب الصيانة، وأعد تقييم البديل عند تغيّر الحالة.
4. **فحص التبعيات:** شغّل [OSV-Scanner](https://github.com/google/osv-scanner) (Apache-2.0) على ملفات manifests وlockfiles المدعومة، وثبّت الإصدارات. يستخدم قاعدة OSV ومعلومات الحزم؛ وثائقه توضح ما قد يُرسل إلى الخدمات، وتوفر نمط offline. لا تعتمد على SCA من Claude وحده، ولا تفترض تغطية ملف غير مدعوم. اجعل الإصلاح التلقائي/تحديث الحزم خارج صلاحية الوكيل، مع مراجعة بشرية وتشغيل اختبارات.
5. **قرار الدمج:** اربط نتائج الفاحصات الحتمية بسياسة فرع: مانع للأسرار الجديدة والثغرات عالية الخطورة القابلة للمعالجة (وفق سياسة المشروع)، واستثناءات مكتوبة بمالك وتاريخ انتهاء. مخرجات Claude إنذار ومراجعة مساعدة؛ لا تحول «لم يجد» إلى «آمن». راجع تغيرات قواعد المهارات ومصادرها ككود طرف ثالث قبل التثبيت، وثبّت SHA/نسخة، وافحص hooks والسكريبتات والأذونات.

## الرخص وإعادة الاستخدام

رخص **MIT** و**Apache-2.0** في الموارد المذكورة رخص برمجيات مألوفة، مع الالتزام بإشعاراتها. أما **CC BY 4.0** و**CC BY-SA 4.0** فهي رخص محتوى/أعمال معرفية وليست رخصة برمجيات تقليدية معتمدة من OSI؛ تسمحان بإعادة الاستخدام بشروط الإسناد، وتضيف BY-SA المشاركة بالمثل للأعمال المشتقة الموزعة. لذلك: احفظ الإسناد وسجل المصدر والتعديلات، وراجع `THIRD_PARTY_NOTICES`، واستشر سياسة التراخيص الداخلية قبل نسخ نصوص هذه المهارات إلى منتج مغلق أو إعادة توزيعها. وصف المشروع بأنه «مفتوح» لا يلغي شروط مكوناته التابعة.

## المصادر الأولية

- [OWASP Secure Agent Playbook: README والكتالوج](https://github.com/OWASP/secure-agent-playbook) · [الرخصة](https://github.com/OWASP/secure-agent-playbook/blob/main/LICENSE.md) · [مهارات أمن الكود](https://github.com/OWASP/secure-agent-playbook/tree/main/plugins/code-security-skills/skills)
- [Trail of Bits Skills](https://github.com/trailofbits/skills) · [مدقق سلسلة التوريد وقيوده](https://github.com/trailofbits/skills/tree/main/plugins/supply-chain-risk-auditor) · [الرخصة](https://github.com/trailofbits/skills/blob/main/LICENSE) · [السوق المنقح](https://github.com/trailofbits/skills-curated)
- [UnitOneAI SecuritySkills](https://github.com/UnitOneAI/SecuritySkills) · [مهارات AppSec](https://github.com/UnitOneAI/SecuritySkills/tree/main/skills/appsec) · [الرخصة](https://github.com/UnitOneAI/SecuritySkills/blob/main/LICENSE)
- [OWASP Security for Claude Code](https://github.com/agamm/claude-code-owasp) · [الرخصة](https://github.com/agamm/claude-code-owasp/blob/main/LICENSE)
- [Anthropic Claude Code Security Review](https://github.com/anthropics/claude-code-security-review) · [إعلان Anthropic](https://www.anthropic.com/news/automate-security-reviews-with-claude-code)
- [Gitleaks](https://github.com/gitleaks/gitleaks) · [Google OSV-Scanner](https://github.com/google/osv-scanner)
- [OWASP Top 10 الرسمي: إصدار 2025](https://owasp.org/www-project-top-ten/)
