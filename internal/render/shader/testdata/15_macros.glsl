#define GAIN 0.8
#define SQUARE(x) ((x)*(x))
void mainImage(out vec4 c, in vec2 p) {
vec2 uv=p/iResolution.xy; float v=SQUARE(uv.x)*GAIN; c=vec4(vec3(v),1.0);
}
