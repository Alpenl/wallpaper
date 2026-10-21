// 最小 ESLint 配置：GJS 运行时全局 + 推荐规则。用法：见 README.md
export default [
    {
        files: ['**/*.js'],
        languageOptions: {
            ecmaVersion: 2022,
            sourceType: 'module',
            globals: {
                global: 'readonly', log: 'readonly', logError: 'readonly', print: 'readonly', printerr: 'readonly',
                imports: 'readonly', TextDecoder: 'readonly', TextEncoder: 'readonly', console: 'readonly',
            },
        },
        rules: {
            'no-undef': 'error',
            'no-unused-vars': ['error', {argsIgnorePattern: '^_', varsIgnorePattern: '^_'}],
            'no-unreachable': 'error',
            'no-dupe-keys': 'error',
            'no-redeclare': 'error',
            'no-const-assign': 'error',
            'prefer-const': 'error',
            eqeqeq: 'error',
        },
    },
];
