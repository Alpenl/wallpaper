float2x2 wp_mat2(float s) { return float2x2(s, 0.0, 0.0, s); }
float3x3 wp_mat3(float s) { return float3x3(s, 0.0, 0.0, 0.0, s, 0.0, 0.0, 0.0, s); }
float4x4 wp_mat4(float s) { return float4x4(s, 0.0, 0.0, 0.0, 0.0, s, 0.0, 0.0, 0.0, 0.0, s, 0.0, 0.0, 0.0, 0.0, s); }
cbuffer WPUniforms : register(b0) { float4 wp_resolutionTime; float4 wp_deltaFrame; float4 iMouse; float4 iDate; float4 wp_audio[128]; };
#define iResolution wp_resolutionTime.xyz
#define iTime wp_resolutionTime.w
#define iTimeDelta wp_deltaFrame.x
#define iFrame asint(wp_deltaFrame.y)
#define iAudioLevel wp_deltaFrame.z
float3 palette(in float t) {
return float3(t, 0.5, (1.0 - t));
}
void adjust(inout float3 v) {
(v *= 0.8);
}
void mainImage(out float4 c, in float2 p) {
float3 col = palette((p.x / iResolution.x));
adjust(col);
(c = float4(col, 1.0));
}
void wp_init() {;}

float4 wp_vertex(uint wp_id : SV_VertexID) : SV_Position { return float4(wp_id == 1 ? 3.0 : -1.0, wp_id == 2 ? 3.0 : -1.0, 0.0, 1.0); }
float4 wp_fragment(float4 wp_pos : SV_Position) : SV_Target { float4 wp_color=0.0; wp_init(); mainImage(wp_color,float2(wp_pos.x,iResolution.y-wp_pos.y)); return wp_color; }
