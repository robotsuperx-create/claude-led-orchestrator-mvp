# سيرفر الـ GPU لـ X-ROBOT-AI

ملف واحد يجهّز السيرفر الذي تستأجره بالساعة. يثبّت عليه ComfyUI للصور والفيديو والموسيقى، ويضع أمامه بوابة بعنوان واحد ورمز سري واحد.

1. استأجر سيرفراً فيه كرت NVIDIA. مثلاً pod على RunPod بقالب PyTorch، وافتح فيه منفذ HTTP رقم 8443.
2. نفّذ هذه الأوامر في طرفيته:
   ```bash
   curl -fsSL https://raw.githubusercontent.com/robotsuperx-create/claude-led-orchestrator-mvp/main/deploy/gpu-server/setup.sh -o setup.sh
   bash setup.sh                 # الصور وأدوات التعديل
   WITH_VIDEO=1 bash setup.sh    # ومعها نماذج الفيديو (حوالي 25 GB إضافية)
   ```
   الموسيقى والمؤثرات تحتاج رمز Hugging Face: `HF_TOKEN=... bash setup.sh`، بعد قبول رخصة Stable Audio Open على huggingface.co.
3. في آخر التنفيذ يطبع السكربت الرمز السري. ضعه هو والعنوان في X-ROBOT-AI ← الإعدادات ← Mission control ← سيرفر الـ GPU. عنوان RunPod يكون على هذا الشكل: `https://<pod id>-8443.proxy.runpod.net`.
4. لكي يشغّل التطبيق السيرفر ويطفئه وحده، ضع في نفس الشاشة معرّف الـ pod ومفتاح RunPod API، والسعر في الساعة، ومدة الخمول بالدقائق.

**ملاحظات**
- صوت X-00 عبر السيرفر يحتاج خادم كلام متوافق مع OpenAI على المنفذ 8000، مثل speaches. البوابة توجّه إليه كل ما يبدأ بـ `/v1`.
- السكربت لم يُجرَّب بعد على سيرفر حقيقي. إن ظهر خطأ، أرسل لي آخر سطور ملف `comfyui.log` لأصلحه.

---

**English.** `setup.sh` installs ComfyUI and the models the built-in capabilities use. It puts a Caddy gate on port 8443 that requires `Authorization: Bearer <token>`: `/v1/*` goes to a speech server on 8000 and everything else to ComfyUI. It prints the token to paste into the app. The script is idempotent; run it again after a restart. It has not yet been run on real hardware.
