# مستودعات Skills وAgents قوية لهندسة البرمجيات

**أفضل نقطة انطلاق لتنسيق تنفيذ متعدد الملفات هي [obra/superpowers](https://github.com/obra/superpowers):** يجمع التخطيط، وعزل العمل بـGit worktrees، وTDD، والتنفيذ بالمساعدين الفرعيين، ومراجعة مرحلية في منهج واحد. إذا كان المطلوب أن تكون المهام المستقلة متوازية فعليًا، فإن [mattpocock/skills](https://github.com/mattpocock/skills) يشرح بوضوح مخطط تذاكر واعتماديات وعمالًا في worktrees منفصلة. ولمن يريد حزمة أوسع ومتعددة المنصات، يبرز [wshobson/agents](https://github.com/wshobson/agents). لا يوجد مستودع واحد يتفوق في كل محور: Superpowers هو الأوضح في worktrees وTDD، Matt في orchestration لتذاكر متوازية، وAddy في دورة هندسية متكاملة مع مراجعة ذات معايير صريحة.

فُحصت ملفات README وLICENSE وبيانات GitHub لكل مشروع. النشاط هنا هو تاريخ آخر `pushed_at` ظاهر في GitHub API، وعدد النجوم لقطة متغيرة وقت الفحص (5 أكتوبر 2026) وليست مقياس جودة أو أداء. جميع المستودعات السبعة تعرض MIT في رخصة الجذر؛ لكن ذلك لا يجعل بالضرورة المواد أو الأصول الخارجية المرتبطة داخل القوائم مرخصة بالـMIT. افحص رخصة كل skill من مصدره قبل نسخه أو توزيعه.

## المقارنة

### 1. [obra/superpowers](https://github.com/obra/superpowers) — أفضل أساس لسير عمل مضبوط من البداية للنهاية

هو إطار skills ومنهج تطوير، لا مجرد مجموعة prompts. يربط التصميم بخطة مهام صغيرة ذات مسارات ملفات وخطوات تحقق، ثم يتيح التنفيذ عبر subagents أو التنفيذ المتتابع، مع مراجعة بين المهام. ومن الأصول المباشرة [using-git-worktrees](https://github.com/obra/superpowers/blob/main/skills/using-git-worktrees/SKILL.md)، و[test-driven-development](https://github.com/obra/superpowers/blob/main/skills/test-driven-development/SKILL.md)، و[requesting-code-review](https://github.com/obra/superpowers/blob/main/skills/requesting-code-review/SKILL.md). يدعم README عددًا كبيرًا من أدوات البرمجة، ويصف دورة استخدام تشمل brainstorming، إنشاء worktree وفحص خط أساس الاختبارات، ثم كتابة الخطة والتنفيذ والمراجعة. [1]

اقتباسان قصيران صالحان للاقتباس: “Detect existing isolation first. Then use native tools. Then fall back to git. Never fight the harness.” [4] و“NO PRODUCTION CODE WITHOUT A FAILING TEST FIRST”. [5] هذا يجعله الأكثر مباشرة عندما تكون متطلباتك worktree قبل التنفيذ، واختبارًا فاشلًا مثبتًا قبل كود الإنتاج، ومراجعة قبل الدمج. MIT؛ **295,142 نجمة**؛ آخر دفع مرصود **27 سبتمبر 2026**. [2] [3]

**المقايضة:** المنهج صريح وحازم، وقد يضيف خطوات إلى تغيير صغير أو إلى فريق لا يريد اعتماد دورة العمل بأكملها. وهو مرجع لتصميم المهارات أكثر من كونه تنفيذًا ذاتيًا مستقلًا عن إمكانات الـharness؛ تكييف مسارات الملفات/الأدوات يبقى مهمًا.

### 2. [mattpocock/skills](https://github.com/mattpocock/skills) — أقوى مثال لعمل متعدد التذاكر والمتوازي

حزمة صغيرة قابلة للتركيب، مع `/tdd` للتطوير red–green–refactor، و`/code-review`، و`/implement-spec` لتنفيذ مواصفة موزعة إلى تذاكر ذات علاقات حجب. يستحق [implement-spec](https://github.com/mattpocock/skills/blob/main/skills/engineering/implement-spec/SKILL.md) اهتمامًا خاصًا: ينشئ فرع تكامل، ويوزع التذاكر الجاهزة على implementer subagents، ويستخدم worktree وفرعًا لكل منفّذ، ثم يدمج النتائج ويطلب مراجعة على فرع التكامل وينظف worktrees. يذكر النص: “The tickets are not a list of steps. They are a task graph with blocking relationships between them.” [29]

مناسب حين تكون التغييرات موزعة على وحدات مستقلة ويمكن صياغتها كتذاكر ذات اعتماديات. README يشدد كذلك على اختبارات حقيقية وتغذية راجعة سريعة، ويعرض مهارات لمعمارية الشيفرة، والتشخيص، والمواصفات والتذاكر. [25] [28] MIT؛ **275,736 نجمة**؛ آخر دفع **4 أكتوبر 2026**. [26] [27]

**المقايضة:** سير العمل يفترض وجود متتبع تذاكر وإعداد أولي للمشروع؛ لا يقدم README حزمة عامة بنفس صراحة Superpowers لتحديد worktree محلي اختياري قبل كل تغيير. توجد أيضًا اختلافات في دعم الـharness، ويذكر README أن الـCodex plugin الأصلي على خارطة الطريق. [25] لا ينبغي اعتماد عدد النجوم بدل اختبار المهارات في بيئة المشروع.

### 3. [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills) — أفضل حزمة lifecycle مع مراجعة هندسية متعددة المحاور

README يصف **25 skill** و**9 أوامر** تمتد من `/spec` و`/plan` إلى `/build` و`/test` و`/review` و`/ship`. للاستفادة المباشرة: [test-driven-development](https://github.com/addyosmani/agent-skills/blob/main/skills/test-driven-development/SKILL.md) يطلب اكتشاف أوامر الاختبار والأعراف الموجودة قبل بدء الحلقة، ثم الاختبار المركز ومجموعة المشروع الكاملة؛ و[code-review-and-quality](https://github.com/addyosmani/agent-skills/blob/main/skills/code-review-and-quality/SKILL.md) يراجع الصحة، والوضوح، والمعمارية، والأمن، والأداء. كما يذكر README مهارة `implement-spec` التي تشغّل implementer subagents على جبهة التذاكر الجاهزة وتنهي بـcode review. اقتباس TDD: “Tests are proof — ‘seems right’ is not done.” [7] [10]

ميزة الحزمة سهولة تركيب المهارات على أدوات كثيرة، ووضوح دورة التسليم، وتغطية refactoring والمراجعة لا مجرد توليد الشيفرة. MIT؛ **101,081 نجمة**؛ آخر دفع **3 أكتوبر 2026**. [8] [9]

**المقايضة:** README يوثق فجوة قابلية نقل: تركيب skill مفرد بـ`npx skills` لا ينسخ مجلد `references/` المشترك، فتتعطل إحالات بعض المهارات إلى قوائم الفحص ما لم يدمجها المستخدم يدويًا. عالجها باختبار تركيب فعلي قبل اعتماد الحزمة على نطاق الفريق. [7]

### 4. [wshobson/agents](https://github.com/wshobson/agents) — أفضل كتالوج تنفيذي عابر للأدوات

بحسب README، يجمع **94 plugin** (92 محليًا و2 خارجيين)، و**202 agent**، و**184 skill**، و**105 command** تحت مصدر Markdown واحد، مع adapters لـClaude Code وCodex وCursor وOpenCode وGitHub Copilot وAntigravity وPi. تتضمن الأمثلة `python-development` للاختبار والتغليف، و`developer-essentials` للمراجعة وتصحيح الأخطاء وGit والاختبار. ومن المكونات القابلة للفحص [python-testing-patterns](https://github.com/wshobson/agents/tree/main/plugins/python-development/skills/python-testing-patterns) و[فهرس مهارات agents](https://github.com/wshobson/agents/blob/main/docs/agent-skills.md). التحقق آليًا من البنية والمخرجات المولدة متاح بأوامر `make generate-all` و`make validate STRICT=1`؛ أما LLM judge وMonte Carlo فموسومان تجريبيين ولا تثبت درجاتهما النجاح في مشروع حقيقي. [12]

هذا مرشح قوي لمن يهمه تعدد الـharness واختيار agent متخصص أو plugin حسب اللغة. MIT؛ **40,200 نجمة**؛ آخر دفع **4 أكتوبر 2026**. [13] [14]

**المقايضة:** الحجم وطبقة التحويل بين تنسيقات الأدوات يعنيان تكلفة اختيار وصيانة أكبر من skill منفردة. README يحدد متطلبات لبعض مسارات التثبيت مثل Python 3.12+ و`uv`؛ كما أن المكونات الخارجية قد تتبع تراخيص ومتطلبات مستقلة، فينبغي مراجعة كل plugin مطلوب بدل افتراض أن طريقة تثبيت واحدة تغطي كل شيء. [12]

### 5. [garrytan/gstack](https://github.com/garrytan/gstack) — قوي في مراجعة الفروع وQA والإصدار

حزمة أدوار وأوامر عملية تركز على ما قبل وبعد تنفيذ التغيير: `/plan-eng-review`، و`/review`، و`/test-audit`، و`/qa`، و`/ship`، و`/land-and-deploy` وغيرها. مفيد عندما تحتاج مراجعة diff وQA تشغيليًا ثم مسار شحن، أكثر من حاجتك إلى مكتبة محايدة من قواعد هندسة البرمجيات. يصف README تثبيت Claude Code بأنه الأكثر نضجًا؛ ويعرض Codex وOpenCode وCursor وغيرها كمسارات تجريبية أو تعليمات فقط، مع اعتماديات منها Git وBun. [22]

MIT؛ **135,039 نجمة**؛ آخر دفع **4 أكتوبر 2026**. [23] [24]

**المقايضة:** تركيزه الأوضح على Claude Code، مع تفاوت نضج الـhosts، وبعض الأوامر تحتاج أدوات وبيئة إضافية. لا يظهر worktree isolation وTDD، من README، كالسلسلة المركزية الصريحة التي يقدمها Superpowers أو Matt؛ لذلك هو مكمل جيد لمراجعة/QA/release لا اختياري الأول كـorchestrator عام.

### 6. [github/awesome-copilot](https://github.com/github/awesome-copilot) — أفضل مرجع موجه إلى GitHub Copilot

مجموعة مساهمات تضم agents وتعليمات ومهارات وhooks وworkflows وplugins، مع كتالوج للبحث ومثبت plugins في Copilot. صفحة [مهارات Copilot](https://github.com/github/awesome-copilot/blob/main/docs/README.skills.md) تضم مهارات مثل `agent-skill-stack` لترشيح وتجميع أصغر مجموعة skills متوافقة، و`ai-team-orchestration` لبدء فريق تطوير متعدد الوكلاء، و`ai-ready` لتهيئة المستودع للتعاون مع الوكلاء. [15] [18]

MIT على مستوى المستودع؛ **39,682 نجمة**؛ آخر دفع **1 أكتوبر 2026**. [16] [17]

**المقايضة:** أفضل قيمة له داخل Copilot؛ الأصول المخصصة أو تعليمات التكامل ليست ضمانًا أنها تعمل مباشرة في Claude Code أو Codex. هو كتالوج مفيد لاختيار الوكلاء والإعدادات، لكن يجب فرز المحتوى بحسب البيئة والتحقق من الملف/المكوّن المحدد بدل تنزيل القائمة كاملة.

### 7. [VoltAgent/awesome-agent-skills](https://github.com/VoltAgent/awesome-agent-skills) — أوسع دليل اكتشاف، لا حزمة تشغيل موحدة

README يفهرس **أكثر من 1,497 skill** من فرق تطوير ومساهمين، ويقول إن المجموعة تنتقي المهارات الواقعية بدل توليدها آليًا على نطاق واسع؛ ويغطي مهارات وأدوات كثيرة منها Claude Code وCodex وGemini CLI وCursor وCopilot. تبرز قيمته عندما تبحث عن skill تخصصية أو تريد اكتشاف مصدر رسمي قبل التبني. [19]

رخصة دليل المستودع MIT؛ **35,185 نجمة**؛ آخر دفع **2 أكتوبر 2026**. [20] [21]

**المقايضة:** هذا فهرس وروابط لمصادر متباينة، وليس منهجًا واحدًا ينسق التخطيط والاختبارات والمراجعة أو يضمن أنها تتبع سياسة أمنية أو جودة مشتركة. اعتبر MIT رخصة المستودع/الدليل فقط، ثم تحقق من رخصة المصدر الأصلي لكل skill قبل نسخه أو إعادة توزيعه.

## الترشيح النهائي

لـ**agent orchestrator عام متعدد الملفات**، اجعل Superpowers المرجع الأول لسلسلة التنفيذ: تحقق من العزل الموجود، استخدم الـworktree المناسب، خطط لمهام قابلة للتحقق، نفّذ TDD، واطلب مراجعة في نقاط واضحة. اقتبس من [using-git-worktrees](https://github.com/obra/superpowers/blob/main/skills/using-git-worktrees/SKILL.md) فحوص عزل الـharness ومجلد worktree النظيف؛ ومن [test-driven-development](https://github.com/obra/superpowers/blob/main/skills/test-driven-development/SKILL.md) إثبات فشل RED ونجاح GREEN؛ ومن [requesting-code-review](https://github.com/obra/superpowers/blob/main/skills/requesting-code-review/SKILL.md) تمرير SHAs والسياق المحدد إلى reviewer ومعالجة القضايا بحسب شدتها.

أضف من Matt نمط task-graph/frontier والعمل في worktrees متوازية عندما تتضح علاقات التبعيات، ومن Addy قوائم مراجعة الخمس محاور واكتشاف أوامر الاختبار الخاصة بالمستودع. خذ Wshobson مصدر إلهام لتصميم adapters متعددة الـharness والتحقق من artifacts، لا تنسخ كتالوجه كاملًا إلى orchestrator. استخدم gstack لو كانت الحاجة المحددة تدقيق QA/Review/Ship في Claude Code، وAwesome Copilot لبيئة Copilot، وVoltAgent لاكتشاف مصادر مهارات جديدة.

**أهم تحذير تصميمي:** لا تجمع جميع هذه الحزم كما هي في system prompt واحد. فيها تداخل في تفعيل المهارات، وأسماء الأوامر، ومتطلبات المراجعة، وافتراضات الفروع والتذاكر. انتقِ قواعد قصيرة حاسمة مشتركة، ثم اجعل كل workflow تخصصيًا وقابلًا للتحميل عند الحاجة؛ اختبر الأدوات على تعديلات متعددة الملفات حقيقية قبل تثبيت ترتيب الوكلاء أو ادعاء تحسن الجودة.

## المراجع

[1]: https://raw.githubusercontent.com/obra/superpowers/main/README.md "Superpowers README"
[2]: https://api.github.com/repos/obra/superpowers "GitHub API: obra/superpowers metadata"
[3]: https://raw.githubusercontent.com/obra/superpowers/main/LICENSE "Superpowers MIT license"
[4]: https://raw.githubusercontent.com/obra/superpowers/main/skills/using-git-worktrees/SKILL.md "Superpowers using-git-worktrees skill"
[5]: https://raw.githubusercontent.com/obra/superpowers/main/skills/test-driven-development/SKILL.md "Superpowers test-driven-development skill"
[6]: https://raw.githubusercontent.com/obra/superpowers/main/skills/requesting-code-review/SKILL.md "Superpowers requesting-code-review skill"
[7]: https://raw.githubusercontent.com/addyosmani/agent-skills/main/README.md "Addy Osmani agent-skills README"
[8]: https://api.github.com/repos/addyosmani/agent-skills "GitHub API: addyosmani/agent-skills metadata"
[9]: https://raw.githubusercontent.com/addyosmani/agent-skills/main/LICENSE "Addy Osmani agent-skills MIT license"
[10]: https://raw.githubusercontent.com/addyosmani/agent-skills/main/skills/test-driven-development/SKILL.md "Addy Osmani test-driven-development skill"
[11]: https://raw.githubusercontent.com/addyosmani/agent-skills/main/skills/code-review-and-quality/SKILL.md "Addy Osmani code-review-and-quality skill"
[12]: https://raw.githubusercontent.com/wshobson/agents/main/README.md "wshobson/agents README"
[13]: https://api.github.com/repos/wshobson/agents "GitHub API: wshobson/agents metadata"
[14]: https://raw.githubusercontent.com/wshobson/agents/main/LICENSE "wshobson/agents MIT license"
[15]: https://raw.githubusercontent.com/github/awesome-copilot/main/README.md "GitHub Awesome Copilot README"
[16]: https://api.github.com/repos/github/awesome-copilot "GitHub API: github/awesome-copilot metadata"
[17]: https://raw.githubusercontent.com/github/awesome-copilot/main/LICENSE "GitHub Awesome Copilot MIT license"
[18]: https://raw.githubusercontent.com/github/awesome-copilot/main/docs/README.skills.md "GitHub Awesome Copilot skills catalog"
[19]: https://raw.githubusercontent.com/VoltAgent/awesome-agent-skills/main/README.md "VoltAgent awesome-agent-skills README"
[20]: https://api.github.com/repos/VoltAgent/awesome-agent-skills "GitHub API: VoltAgent/awesome-agent-skills metadata"
[21]: https://raw.githubusercontent.com/VoltAgent/awesome-agent-skills/main/LICENSE "VoltAgent awesome-agent-skills MIT license"
[22]: https://raw.githubusercontent.com/garrytan/gstack/main/README.md "gstack README"
[23]: https://api.github.com/repos/garrytan/gstack "GitHub API: garrytan/gstack metadata"
[24]: https://raw.githubusercontent.com/garrytan/gstack/main/LICENSE "gstack MIT license"
[25]: https://raw.githubusercontent.com/mattpocock/skills/main/README.md "Matt Pocock skills README"
[26]: https://api.github.com/repos/mattpocock/skills "GitHub API: mattpocock/skills metadata"
[27]: https://raw.githubusercontent.com/mattpocock/skills/main/LICENSE "Matt Pocock skills MIT license"
[28]: https://raw.githubusercontent.com/mattpocock/skills/main/skills/engineering/tdd/SKILL.md "Matt Pocock TDD skill"
[29]: https://raw.githubusercontent.com/mattpocock/skills/main/skills/engineering/implement-spec/SKILL.md "Matt Pocock implement-spec skill"
