/* Globals initialize after uniforms, once per fragment. */
float phase = iTime * 0.1;
void mainImage(out vec4 c, vec2 p) {
 vec4 audio = texture(iAudio, vec2(0.5,0.0));
 c = vec4(vec3(fract(phase)) + audio.xyz, 1.0);
}
