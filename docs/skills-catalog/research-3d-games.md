# أفضل خمس مهارات ووكلاء وworkflows مفتوحة المصدر لـ3D والألعاب

**القرار المختصر:** اجعل `awesome-gamedev-agent-skills` قاعدة المعرفة متعددة المحركات، وأضف مسار Three.js للنماذج الأولية القابلة للعب، ومهارة Lottie للحركة، مع تجربة محدودة لمهارات shader. وفّر Blender MCP كتوصيل اختياري ومقيّد الصلاحيات، لا كأداة مفعّلة تلقائيًا. جميع المرشحين الخمسة لديهم ملف ترخيص مفتوح واضح؛ تفاوت نشاطهم كبير. الأرقام والتواريخ أدناه كما ظهرت في GitHub حتى **5 أكتوبر 2026**.

## أفضل خمسة

### 1. [awesome-gamedev-agent-skills](https://github.com/gamedev-skills/awesome-gamedev-agent-skills) — أفضل قاعدة عامة متعددة المحركات

يضم 74 ملف `SKILL.md` وموجّهًا يختار المهارة بحسب المحرك والمهمة. يغطي Godot وUnity وUnreal وPhaser وPixiJS وThree.js وBevy وغيرها، وفيه مهارات للاختبار والتصدير ومظلّلات Godot وإنتاج مجموعات الأصول مع توحيد أسلوبها والتحقق منها داخل اللعبة. ملائم كنواة أولى لمكتبة الوكيل بدل جمع مهارات منفصلة بلا توجيه. [1]

ترخيصه **Apache-2.0**؛ احتفظ بملف الترخيص و`NOTICE` وإشعارات النسب عند إعادة التوزيع. سجّل المستودع 1,309 نجوم و107 commits، وآخر commit منشور في **27 سبتمبر 2026**؛ نشاطه حديث مقارنة ببقية القائمة. [2] [3]

**قرار الدمج:** ادمج انتقائيًا الموجّه والمهارات الموافقة لمحركات المنتج، وثبّت commit محددًا بدل السحب التلقائي من `main`. احتفظ بإشعارات Apache وراجع تحديثات إصدارات المحركات قبل تبني تعليمات مهارة جديدة.

### 2. [MCP for Blender](https://github.com/ahujasid/mcp-for-blender) — أقوى جسر عملي إلى Blender

خادم MCP وإضافة Blender يتيحان للوكيل فحص المشهد وإنشاء الأجسام وتعديل المواد وتشغيل شيفرة Python. يذكر README دعم استيراد أصول من مصادر متعددة وتوليد نماذج. المشروع تكامل مجتمعي مستقل وليس منتجًا رسميًا من Blender. [4]

ملفه **MIT**. للمشروع انتشار كبير (نحو 29.9 ألف نجمة و2,713 fork و233 commit بحسب صفحات GitHub)، وآخر commit في **30 سبتمبر 2026**. [5] [6]

**قرار الدمج:** أضف موصلًا اختياريًا لبيئة Blender محلية مع موافقة واضحة قبل أي تنفيذ لشيفرة Python، وسجّل العمليات، واختبره في ملف/بيئة معزولة. لا تمنحه وصولًا غير محدود إلى ملفات المستخدم؛ README يوضح أن التكامل ينفّذ شيفرة داخل Blender، كما يشرح جمع قياسات استخدام مع خيار تعطيلها. راجع [الشروط والخصوصية](https://github.com/ahujasid/mcp-for-blender/blob/main/TERMS_AND_CONDITIONS.md) قبل تفعيله.

### 3. [Three.js Game Skills](https://github.com/majidmanzarpour/threejs-game-skills) — أفضل مسار لنموذج أولي تفاعلي قابل للعب

حزمة من تسع مهارات Codex وClaude يقودها `threejs-game-director`، وتغطي اللعب والرسوم والواجهة وتوليد 3D والأصول الصوتية والتصحيح وQA والإصدار. تتضمن scaffold بـVite وTypeScript، وقوالب Playwright لاختبارات الدخان واللقطات واللعب الآلي، لذلك لا تكتفي بإنشاء نموذج بل تقرن البناء بالتحقق منه. [7]

الترخيص **MIT**؛ يذكر المستودع 2,425 نجمة و241 fork و11 commit، وآخر commit في **28 سبتمبر 2026**. [8] [9]

**قرار الدمج:** اعتمده كمسار مستقل لـWeb/Three.js prototypes، مع تشغيل اختبارات المتصفح ولقطات العرض ضمن قبول المنتج. خدمات توليد النماذج/الصورة/الصوت اختيارية وقد تتطلب مفاتيح مزوّدين خارجيين؛ لا تجعلها شرطًا للنموذج الأساسي، ولا تحفظ مفاتيحها في ملفات المشروع.

### 4. [LottieFiles Motion Design Skill](https://github.com/LottieFiles/motion-design-skill) — أفضل إضافة نوعية لحركة الواجهات

مهارة عامة لا ترتبط بمكتبة بعينها؛ تصف التوقيت والتسارع والتتابع والإيقاع والشخصية الحركية، وتضم وصفات لحالات التحميل والنجاح والخطأ والانتقالات والـmicro-interactions. تصلح لتعليم الوكيل اتخاذ قرار تصميم الحركة قبل كتابة CSS أو Framer Motion أو GSAP أو Lottie. [10]

الترخيص **MIT**. المستودع لديه 1,868 نجمة و104 forks و4 commits؛ آخر commit في **18 مايو 2026**. الانتشار مرتفع، لكن وتيرة التحديث أقل من المرشحين الأحدث. [11] [12]

**قرار الدمج:** أضفها كمهارة مرجعية خفيفة للحركة، مع ملحق داخلي يلزم احترام تقليل الحركة وإتاحة الاستخدام وأداء الإطارات، واختبار الحركة في الواجهة الحقيقية.

### 5. [Unity Shader Agent Skills](https://github.com/adevra/unity-shader-agent-skills) — مرجع متخصص للـshaders مع شرط التجربة

سبع مهارات لـUnity ShaderLab/HLSL وURP وShader Graph، مع اهتمام بتحسين الأجهزة المحمولة وWebGL، وحزم القوام وتقليل shader variants. هذه أكثر إضافة تخصصًا للمظلّلات ضمن المرشحين الخمسة. [13]

الترخيص **MIT**، لكن سجل النشاط أضعف إشارة في القائمة: 12 نجمة، وcommit واحد فقط يعود إلى **28 مارس 2026**. المحتوى مفيد كنقطة انطلاق وليس دليلًا كافيًا على صيانة متواصلة أو صحة كل إرشاد عبر إصدارات Unity المختلفة. [14] [15]

**قرار الدمج:** لا تدمج الحزمة كاملة كمرجع موثوق قبل تجربة. ابدأ بمهارتين أو ثلاث في branch تجريبي، واربط كل قاعدة بمصدرها، ثم اشترط compilation واختبارات عرض/أداء على URP وWebGL والأجهزة المستهدفة قبل اعتمادها.

## قرار الدمج في المنتج

1. **النواة:** اعتمد مجموعة `awesome-gamedev-agent-skills` كطبقة مهارات عامة، مع تثبيت نسخة ومراجعة الترخيص والإشعارات لكل تحديث. لا تحمّل جميع مهارات المحركات إلى سياق كل طلب؛ استخدم التوجيه لتحميل المطلوب فقط.
2. **مسار النماذج التفاعلية:** أضف حزمة Three.js كباقة منفصلة للبناء القابل للعب وQA البصري. اجعل توليد الأصول الخارجي اختياريًا وافصل إدارة المفاتيح عن مستودع المستخدم.
3. **الأصول ثلاثية الأبعاد:** استخدم مسارًا داخليًا واضحًا: تأليف/تنظيف في Blender، ثم تصدير GLB/glTF، ثم فحص المقاييس والمحاور والأسماء والخامات والحجم والتصادم وLOD، ثم تحقق فعلي داخل المحرك. مهارات #1 و#3 تدعم الإنتاج والتقييم؛ لا تجعل «نجاح التصدير» وحده دليل جاهزية الأصل.
4. **التوصيل والأمان:** اجعل Blender MCP موصلًا اختياريًا لا يعمل إلا بإذن؛ تعامل مع تنفيذ Python كقدرة ذات مخاطر، وافصل ذلك عن نموذج المهارات النصي.
5. **الحركة والـshaders:** يمكن تضمين مهارة Lottie الآن مع اختبارات الحركة وإمكانية الوصول. تعامل مع حزمة Unity shader كتجربة منخفضة الثقة حتى يثبت كل مثال بالبناء والعرض على target الفعلي.

**مستبعد مؤقتًا رغم صلته المباشرة بخط الأصول:** مهارة [Web 3D Asset Pipeline](https://github.com/openai/plugins/blob/main/plugins/game-studio/skills/web-3d-asset-pipeline/SKILL.md) تصف خطوات مفيدة لتصدير GLB/glTF وتحسينه وفحص LOD والتصادم. لكن صفحة المستودع لا تعرض ملف `LICENSE` ضمن ملفات الجذر، ولم يتسن التحقق من ترخيص قابل لإعادة الاستخدام؛ لذا لا تنسخ محتواها إلى المنتج قبل توضيح الترخيص. يمكن استخدام وصفها لتحديد متطلبات داخلية مستقلة، لا بوصفها مادة مرخّصة للدمج. [16] [17]

**ملاحظة تشغيلية:** لم يكن خادم `workflow` متاحًا في جلسة البحث؛ لذلك جرى التحقق مباشرة من صفحات GitHub وملفات README/LICENSE وواجهة commits، دون تفويض البحث لوكلاء فرعيين.

## المراجع

[1]: https://github.com/gamedev-skills/awesome-gamedev-agent-skills "README مستودع awesome-gamedev-agent-skills"
[2]: https://github.com/gamedev-skills/awesome-gamedev-agent-skills/blob/main/LICENSE "ترخيص Apache-2.0"
[3]: https://api.github.com/repos/gamedev-skills/awesome-gamedev-agent-skills/commits?per_page=1 "أحدث commit للمستودع"
[4]: https://github.com/ahujasid/mcp-for-blender "README مستودع MCP for Blender"
[5]: https://github.com/ahujasid/mcp-for-blender/blob/main/LICENSE "ترخيص MIT لـMCP for Blender"
[6]: https://api.github.com/repos/ahujasid/mcp-for-blender/commits?per_page=1 "أحدث commit لـMCP for Blender"
[7]: https://github.com/majidmanzarpour/threejs-game-skills "README مستودع Three.js Game Skills"
[8]: https://github.com/majidmanzarpour/threejs-game-skills/blob/main/LICENSE "ترخيص MIT لـThree.js Game Skills"
[9]: https://api.github.com/repos/majidmanzarpour/threejs-game-skills/commits?per_page=1 "أحدث commit لـThree.js Game Skills"
[10]: https://github.com/LottieFiles/motion-design-skill "README مستودع Motion Design Skill"
[11]: https://github.com/LottieFiles/motion-design-skill/blob/main/LICENSE "ترخيص MIT لـMotion Design Skill"
[12]: https://api.github.com/repos/LottieFiles/motion-design-skill/commits?per_page=1 "أحدث commit لـMotion Design Skill"
[13]: https://github.com/adevra/unity-shader-agent-skills "README مستودع Unity Shader Agent Skills"
[14]: https://github.com/adevra/unity-shader-agent-skills/blob/main/LICENSE "ترخيص MIT لـUnity Shader Agent Skills"
[15]: https://api.github.com/repos/adevra/unity-shader-agent-skills/commits?per_page=1 "أحدث commit لـUnity Shader Agent Skills"
[16]: https://github.com/openai/plugins/blob/main/plugins/game-studio/skills/web-3d-asset-pipeline/SKILL.md "مهارة Web 3D Asset Pipeline"
[17]: https://github.com/openai/plugins "README وقائمة ملفات مستودع OpenAI Plugins"
