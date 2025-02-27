#### Features 🚀

- Container with constant key near attribute now can have descendant objects and connections [#1071](https://github.com/terrastruct/d2/pull/1071)
- Classes are implemented. See [docs](https://d2lang.com/todo). [#772](https://github.com/terrastruct/d2/pull/772)
- Multi-board SVG outputs with internal links go to their output paths [#1116](https://github.com/terrastruct/d2/pull/1116)
- New grid layout to place nodes in rows and columns [#1122](https://github.com/terrastruct/d2/pull/1122)

#### Improvements 🧹

- Labels on parallel `dagre` connections include a gap between them [#1134](https://github.com/terrastruct/d2/pull/1134)
- Sequence Diagram `Lifelines` now inherit the Actor `stroke` and `stroke-dash` [#1140](https://github.com/terrastruct/d2/pull/1140)
- Add `text-transform` styling option [#1118](https://github.com/terrastruct/d2/pull/1118)

#### Bugfixes ⛑️

- Fix inheritence in scenarios/steps [#1090](https://github.com/terrastruct/d2/pull/1090)
- Fix a bug in 32bit builds [#1115](https://github.com/terrastruct/d2/issues/1115)
- Fix a bug in ID parsing [#322](https://github.com/terrastruct/d2/issues/322)
- Fix a bug in watch mode parsing SVG [#1119](https://github.com/terrastruct/d2/issues/1119)
- Namespace transitions so that multiple animated D2 diagrams can exist on the same page [#1123](https://github.com/terrastruct/d2/issues/1123)
- Fix a bug in vertical alignment of appendix lines [#1104](https://github.com/terrastruct/d2/issues/1104)
- Fix precision difference for sketch mode running on different architectures [#921](https://github.com/terrastruct/d2/issues/921)
- Fix an issue where markdown fonts weren't being applied correctly [#1132](https://github.com/terrastruct/d2/issues/1132)

#### Breaking changes

- `class` and `classes` are now reserved keywords.
