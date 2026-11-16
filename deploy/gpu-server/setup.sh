#!/usr/bin/env bash
# X-ROBOT-AI GPU server: ComfyUI for pictures, video and sound, behind one
# address and one token, ready for the app's media gateway.
#
# Run it once on a rented GPU machine (a RunPod pod with a PyTorch template,
# or any Ubuntu machine with an NVIDIA card and Python 3.10+):
#
#   curl -fsSL https://raw.githubusercontent.com/robotsuperx-create/claude-led-orchestrator-mvp/main/deploy/gpu-server/setup.sh -o setup.sh
#   bash setup.sh            # pictures, editing tools and music
#   WITH_VIDEO=1 bash setup.sh   # also the video models (about 25 GB more)
#
# It prints the token at the end: paste the address and the token into
# X-ROBOT-AI > Settings > Mission control > GPU server. On RunPod expose
# HTTP port 8443; the address is https://<pod id>-8443.proxy.runpod.net.
#
# Optional: HF_TOKEN=<your Hugging Face token> for Stable Audio Open (its
# licence must be accepted on huggingface.co first).
set -euo pipefail

ROOT="${XROBOT_ROOT:-/workspace/xrobot}"
COMFY="$ROOT/ComfyUI"
PORT="${XROBOT_PORT:-8443}"
TOKEN_FILE="$ROOT/token"
HF="https://huggingface.co"

mkdir -p "$ROOT"
cd "$ROOT"

if [ ! -s "$TOKEN_FILE" ]; then
	umask 077
	python3 -c 'import secrets; print(secrets.token_hex(24))' > "$TOKEN_FILE"
fi
TOKEN="$(cat "$TOKEN_FILE")"

echo "== ComfyUI"
if [ ! -d "$COMFY" ]; then
	git clone --depth 1 https://github.com/comfyanonymous/ComfyUI "$COMFY"
fi
python3 -m pip install --quiet --upgrade pip
python3 -m pip install --quiet -r "$COMFY/requirements.txt"

fetch() { # fetch <url> <folder> <name>
	mkdir -p "$COMFY/models/$2"
	if [ ! -s "$COMFY/models/$2/$3" ]; then
		echo "   $3"
		if [ -n "${HF_TOKEN:-}" ] && [[ "$1" == "$HF"* ]]; then
			curl -fL --retry 3 -H "Authorization: Bearer $HF_TOKEN" -o "$COMFY/models/$2/$3.part" "$1"
		else
			curl -fL --retry 3 -o "$COMFY/models/$2/$3.part" "$1"
		fi
		mv "$COMFY/models/$2/$3.part" "$COMFY/models/$2/$3"
	fi
}

echo "== Models for pictures and the editing tools"
fetch "$HF/Comfy-Org/flux1-schnell/resolve/main/flux1-schnell-fp8.safetensors" checkpoints flux1-schnell-fp8.safetensors
fetch "https://github.com/xinntao/Real-ESRGAN/releases/download/v0.1.0/RealESRGAN_x4plus.pth" upscale_models RealESRGAN_x4plus.pth

WAN="$HF/Comfy-Org/Wan_2.1_ComfyUI_repackaged/resolve/main/split_files"
if [ "${WITH_VIDEO:-0}" = "1" ]; then
	echo "== Models for video"
	fetch "$WAN/diffusion_models/wan2.1_t2v_1.3B_fp16.safetensors" diffusion_models wan2.1_t2v_1.3B_fp16.safetensors
	fetch "$WAN/diffusion_models/wan2.1_i2v_480p_14B_fp8_e4m3fn.safetensors" diffusion_models wan2.1_i2v_480p_14B_fp8_e4m3fn.safetensors
	fetch "$WAN/text_encoders/umt5_xxl_fp8_e4m3fn_scaled.safetensors" text_encoders umt5_xxl_fp8_e4m3fn_scaled.safetensors
	fetch "$WAN/vae/wan_2.1_vae.safetensors" vae wan_2.1_vae.safetensors
	fetch "$WAN/clip_vision/clip_vision_h.safetensors" clip_vision clip_vision_h.safetensors
fi

if [ -n "${HF_TOKEN:-}" ]; then
	echo "== Models for music and sound effects"
	fetch "$HF/stabilityai/stable-audio-open-1.0/resolve/main/model.safetensors" checkpoints stable-audio-open-1.0.safetensors
	fetch "$HF/google-t5/t5-base/resolve/main/model.safetensors" text_encoders t5-base.safetensors
else
	echo "== Music and sound effects skipped (set HF_TOKEN to add them)"
fi

echo "== The gate: one address, one token"
if [ ! -x "$ROOT/caddy" ]; then
	curl -fL --retry 3 -o "$ROOT/caddy" "https://caddyserver.com/api/download?os=linux&arch=amd64"
	chmod 755 "$ROOT/caddy"
fi
cat > "$ROOT/Caddyfile" <<CADDY
{
	admin off
	auto_https off
}
:$PORT {
	@denied not header Authorization "Bearer {\$XROBOT_TOKEN}"
	respond @denied 401
	# X-00's voice, when an OpenAI-compatible speech server runs on 8000.
	handle /v1/* {
		reverse_proxy 127.0.0.1:8000
	}
	handle {
		reverse_proxy 127.0.0.1:8188
	}
}
CADDY

echo "== Starting"
pkill -f "ComfyUI/main.py" 2>/dev/null || true
pkill -f "$ROOT/caddy run" 2>/dev/null || true
nohup python3 "$COMFY/main.py" --listen 127.0.0.1 --port 8188 > "$ROOT/comfyui.log" 2>&1 &
XROBOT_TOKEN="$TOKEN" nohup "$ROOT/caddy" run --config "$ROOT/Caddyfile" --adapter caddyfile > "$ROOT/caddy.log" 2>&1 &

for _ in $(seq 1 60); do
	if curl -fs -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:$PORT/system_stats" > /dev/null; then
		echo
		echo "Ready. In X-ROBOT-AI > Settings > Mission control > GPU server:"
		echo "  Server address: https://<this machine's public address for port $PORT>"
		echo "  Server token:   $TOKEN"
		echo "(Run this script again after a restart: it skips what is already there.)"
		exit 0
	fi
	sleep 5
done
echo "ComfyUI did not answer; see $ROOT/comfyui.log" >&2
exit 1
