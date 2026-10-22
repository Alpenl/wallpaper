void mainImage(out vec4 c, in vec2 p) {
vec2 q=p-0.5*iResolution.xy; float angle=atan(q.y,q.x); c=vec4(vec3(fract(angle+iTime)),1.0);
}
