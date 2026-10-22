float2x2 wp_mat2(float s) { return float2x2(s, 0.0, 0.0, s); }
float3x3 wp_mat3(float s) { return float3x3(s, 0.0, 0.0, 0.0, s, 0.0, 0.0, 0.0, s); }
float4x4 wp_mat4(float s) { return float4x4(s, 0.0, 0.0, 0.0, 0.0, s, 0.0, 0.0, 0.0, 0.0, s, 0.0, 0.0, 0.0, 0.0, s); }
float2x2 wp_constructor0(float2 a0, float a1, float a2) { return transpose(float2x2(float(a0.x), float(a0.y), float(a1), float(a2))); }

float4x4 wp_constructor1(float2x2 a0) { return transpose(float4x4(a0[0][0], a0[1][0], 0.0, 0.0, a0[0][1], a0[1][1], 0.0, 0.0, 0.0, 0.0, 1.0, 0.0, 0.0, 0.0, 0.0, 1.0)); }

float3x3 wp_constructor2(float4x4 a0) { return transpose(float3x3(a0[0][0], a0[1][0], a0[2][0], a0[0][1], a0[1][1], a0[2][1], a0[0][2], a0[1][2], a0[2][2])); }
cbuffer WPUniforms : register(b0) { float4 wp_resolutionTime; float4 wp_deltaFrame; float4 iMouse; float4 iDate; float4 wp_audio[128]; };
#define iResolution wp_resolutionTime.xyz
#define iTime wp_resolutionTime.w
#define iTimeDelta wp_deltaFrame.x
#define iFrame asint(wp_deltaFrame.y)
#define iAudioLevel wp_deltaFrame.z
void mainImage(out float4 c, float2 p) {
float2x2 base = wp_constructor0(float2(1.0, 2.0), 3.0, 4.0);
float4x4 large = wp_constructor1(base);
float3x3 reduced = wp_constructor2(large);
float3 col = transpose(reduced)[1];
(c = float4(col, 1.0));
}
void wp_init() {;}

float4 wp_vertex(uint id : SV_VertexID) : SV_Position { return float4(id == 1 ? 3.0 : -1.0, id == 2 ? 3.0 : -1.0, 0.0, 1.0); }
float4 wp_fragment(float4 pos : SV_Position) : SV_Target { float4 color=0.0; wp_init(); mainImage(color,float2(pos.x,iResolution.y-pos.y)); return color; }
