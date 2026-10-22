float2x2 wp_mat2(float s) { return float2x2(s, 0.0, 0.0, s); }
float3x3 wp_mat3(float s) { return float3x3(s, 0.0, 0.0, 0.0, s, 0.0, 0.0, 0.0, s); }
float4x4 wp_mat4(float s) { return float4x4(s, 0.0, 0.0, 0.0, 0.0, s, 0.0, 0.0, 0.0, 0.0, s, 0.0, 0.0, 0.0, 0.0, s); }
float2x2 wp_constructor0(float a0, float a1, float a2, float a3) { return transpose(float2x2(float(a0), float(a1), float(a2), float(a3))); }
cbuffer WPUniforms : register(b0) { float4 wp_resolutionTime; float4 wp_deltaFrame; float4 iMouse; float4 iDate; float4 wp_audio[128]; };
#define iResolution wp_resolutionTime.xyz
#define iTime wp_resolutionTime.w
#define iTimeDelta wp_deltaFrame.x
#define iFrame asint(wp_deltaFrame.y)
#define iAudioLevel wp_deltaFrame.z
void mainImage(out float4 c, in float2 p) {
float a = iTime;
float2x2 r = wp_constructor0(cos(a), sin(a), -sin(a), cos(a));
float2 q = mul(r, (((p / iResolution.xy) - 0.5)));
(c = float4((q + 0.5), 0.0, 1.0));
}
void wp_init() {;}

float4 wp_vertex(uint id : SV_VertexID) : SV_Position { return float4(id == 1 ? 3.0 : -1.0, id == 2 ? 3.0 : -1.0, 0.0, 1.0); }
float4 wp_fragment(float4 pos : SV_Position) : SV_Target { float4 color=0.0; wp_init(); mainImage(color,float2(pos.x,iResolution.y-pos.y)); return color; }
